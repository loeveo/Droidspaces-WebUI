package web

import (
	"context"
	"fmt"
	"log"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/ravindu644/droidspaces-oss/webui/internal/config"
	"github.com/ravindu644/droidspaces-oss/webui/internal/workspace"
)

const nestedAndroidNATLocalTable = 97

type natCompatCommandRunner func(context.Context, string, ...string) (string, error)

// nestedAndroidNATScope describes the one shared policy-rule set for a
// network namespace. The selectors necessarily cover the shared Droidspaces
// NAT CIDR, so separate per-workspace priorities would not isolate traffic:
// the lowest priority rule would capture every workspace's packets. workspace
// is retained only to locate this WebUI's running containers before setup.
type nestedAndroidNATScope struct {
	workspace      string
	toSubnetRule   int
	tetherRule     int
	fromSubnetRule int
}

func nestedAndroidNATScopeForWorkspace(workspacePath string) nestedAndroidNATScope {
	workspacePath = strings.TrimSpace(workspacePath)
	if workspacePath == "" {
		workspacePath = "/var/lib/Droidspaces"
	}
	// Keep these below Android netd's usual 10000+ range and above the official
	// core's 6090/6095/6100 priorities. This also takes precedence over the
	// short-lived 7000+ priorities used by pre-release WebUI builds without
	// needing to remove a rule we cannot safely prove we own. A collision is
	// detected and refused; the WebUI never replaces a non-compatible rule.
	const base = 6200
	return nestedAndroidNATScope{
		workspace:      workspacePath,
		toSubnetRule:   base,
		tetherRule:     base + 5,
		fromSubnetRule: base + 10,
	}
}

// reconcileNestedAndroidNATCompat supplies the Android policy-routing part
// that an official core intentionally skips when it runs inside a normal
// Linux container. It is opt-in because policy rules are shared by the host
// network namespace. It deliberately does not alter sysctl values, firewall
// tables, NAT/DNAT rules, DS-owned routes/chains, or container/image files.
// Forwarding policy remains the responsibility of the outer Droidspaces
// instance: nested Linux containers can be permitted to read Android's legacy
// filter table without permission to modify it.
func (s *Server) reconcileNestedAndroidNATCompat(ctx context.Context) error {
	s.nestedAndroidNATCompatExecMu.Lock()
	defer s.nestedAndroidNATCompatExecMu.Unlock()

	enabled, scope := s.nestedAndroidNATCompatState()
	if !enabled {
		return nil
	}
	running, err := hasRunningNATContainers(scope.workspace)
	if err != nil {
		return fmt.Errorf("inspect running NAT containers before nested Android NAT setup: %w", err)
	}
	if !running {
		// Do not remove shared rules here. Another WebUI instance in the same
		// network namespace may manage a different workspace in the same NAT
		// CIDR. Rules are runtime-only and become relevant again only when a
		// NAT container is started.
		return nil
	}
	table, err := s.androidDefaultRouteTable(ctx)
	if err != nil {
		// Android can temporarily have no active default network during a
		// Wi-Fi/mobile handoff. Keep the local/tether rules, but remove only
		// this scope's stale uplink lookup so traffic is never sent to an old
		// Android route table.
		if cleanupErr := s.removeNestedAndroidNATUplinkRuleLocked(ctx, scope); cleanupErr != nil {
			return cleanupErr
		}
		return err
	}
	return s.installNestedAndroidNATPolicyRules(ctx, scope, table)
}

func (s *Server) removeNestedAndroidNATUplinkRuleLocked(ctx context.Context, scope nestedAndroidNATScope) error {
	rules, err := s.runNATCompatCommand(ctx, "ip", "-4", "rule", "show")
	if err != nil {
		return fmt.Errorf("read policy rules for nested Android NAT cleanup: %w", err)
	}
	for _, table := range nestedAndroidNATFromRuleTables(rules, scope) {
		if err := s.removeNATCompatRule(ctx, scope.fromSubnetRule, "from", config.DefaultNATCIDR, "lookup", strconv.Itoa(table)); err != nil {
			return err
		}
	}
	return nil
}

func hasRunningNATContainers(workspacePath string) (bool, error) {
	snapshot, err := workspace.ReadSnapshot(workspacePath, true)
	if err != nil {
		return false, err
	}
	for _, container := range snapshot.Containers {
		if !container.Running || !strings.EqualFold(strings.TrimSpace(container.NetMode), "nat") || container.PID <= 0 {
			continue
		}
		if err := syscall.Kill(int(container.PID), 0); err == nil || err == syscall.EPERM {
			return true, nil
		}
	}
	return false, nil
}

func (s *Server) androidDefaultRouteTable(ctx context.Context) (int, error) {
	rules, err := s.runNATCompatCommand(ctx, "ip", "-4", "rule", "show")
	if err != nil {
		return 0, fmt.Errorf("read policy rules: %w", err)
	}
	table, ok := parseAndroidDefaultRouteTable(rules)
	if !ok {
		return 0, fmt.Errorf("Android default-network policy rule was not found; nested Android NAT compatibility was not applied")
	}
	routes, err := s.runNATCompatCommand(ctx, "ip", "-4", "route", "show", "table", strconv.Itoa(table))
	if err != nil {
		return 0, fmt.Errorf("read Android route table %d: %w", table, err)
	}
	if !hasDefaultIPv4Route(routes) {
		return 0, fmt.Errorf("Android route table %d has no IPv4 default route; nested Android NAT compatibility was not applied", table)
	}
	return table, nil
}

func (s *Server) installNestedAndroidNATPolicyRules(ctx context.Context, scope nestedAndroidNATScope, table int) error {
	rules, err := s.runNATCompatCommand(ctx, "ip", "-4", "rule", "show")
	if err != nil {
		return fmt.Errorf("read policy rules before nested Android NAT setup: %w", err)
	}
	for _, existingTable := range nestedAndroidNATFromRuleTables(rules, scope) {
		if existingTable == table {
			continue
		}
		if err := s.removeNATCompatRule(ctx, scope.fromSubnetRule, "from", config.DefaultNATCIDR, "lookup", strconv.Itoa(existingTable)); err != nil {
			return err
		}
	}
	// A handoff may have left a previous from-subnet lookup behind. Re-read
	// after deletion so priority collision checks use current state.
	rules, err = s.runNATCompatCommand(ctx, "ip", "-4", "rule", "show")
	if err != nil {
		return fmt.Errorf("re-read policy rules before nested Android NAT setup: %w", err)
	}

	wantedRules := []nestedAndroidNATPolicyRule{
		{priority: scope.toSubnetRule, selector: "to", table: "main"},
		{priority: scope.tetherRule, selector: "from", table: strconv.Itoa(nestedAndroidNATLocalTable)},
		{priority: scope.fromSubnetRule, selector: "from", table: strconv.Itoa(table)},
	}
	// Detect every priority collision before writing the first rule. This avoids
	// leaving a partial compatibility set behind when a host component owns a
	// later priority.
	for _, rule := range wantedRules {
		if !nestedAndroidNATPolicyRulePresent(rules, rule) && nestedAndroidNATPolicyRulePriorityUsed(rules, rule.priority) {
			return fmt.Errorf("nested Android NAT policy priority %d is already used by a non-WebUI rule", rule.priority)
		}
	}
	for _, rule := range wantedRules {
		if err := s.ensureNATCompatPolicyRule(ctx, rules, rule); err != nil {
			return err
		}
	}
	return nil
}

type nestedAndroidNATPolicyRule struct {
	priority int
	selector string
	table    string
}

func (s *Server) ensureNATCompatPolicyRule(ctx context.Context, currentRules string, rule nestedAndroidNATPolicyRule) error {
	if nestedAndroidNATPolicyRulePresent(currentRules, rule) {
		return nil
	}
	if nestedAndroidNATPolicyRulePriorityUsed(currentRules, rule.priority) {
		return fmt.Errorf("nested Android NAT policy priority %d is already used by a non-WebUI rule", rule.priority)
	}
	args := []string{"-4", "rule", "add", "pref", strconv.Itoa(rule.priority), rule.selector, config.DefaultNATCIDR, "lookup", rule.table}
	if _, err := s.runNATCompatCommand(ctx, "ip", args...); err != nil {
		// Another WebUI process using the same workspace can add the same
		// compatible rule between our read and add. Re-read before treating it
		// as an error; a different rule at this priority is still rejected.
		if latestRules, readErr := s.runNATCompatCommand(ctx, "ip", "-4", "rule", "show"); readErr == nil && nestedAndroidNATPolicyRulePresent(latestRules, rule) {
			return nil
		}
		return fmt.Errorf("install nested Android NAT policy rule: %w", err)
	}
	return nil
}

func (s *Server) removeNATCompatRule(ctx context.Context, priority int, selector, cidr, lookup, table string) error {
	rules, err := s.runNATCompatCommand(ctx, "ip", "-4", "rule", "show")
	if err != nil {
		return fmt.Errorf("read policy rules before nested Android NAT cleanup: %w", err)
	}
	wanted := nestedAndroidNATPolicyRule{priority: priority, selector: selector, table: table}
	if !nestedAndroidNATPolicyRulePresent(rules, wanted) {
		return nil
	}
	args := []string{"-4", "rule", "del", "pref", strconv.Itoa(priority), selector, cidr, lookup, table}
	if _, err := s.runNATCompatCommand(ctx, "ip", args...); err != nil && !natCompatNotFound(err) {
		return fmt.Errorf("remove stale nested Android NAT uplink policy rule: %w", err)
	}
	return nil
}

func (s *Server) runNATCompatCommand(ctx context.Context, command string, args ...string) (string, error) {
	if s.nestedAndroidNATCommand != nil {
		return s.nestedAndroidNATCommand(ctx, command, args...)
	}
	path := nestedAndroidNATCommandPath(command)
	cmd := exec.CommandContext(ctx, path, args...)
	cmd.Env = s.terminalEnv()
	out, err := cmd.CombinedOutput()
	text := strings.TrimSpace(string(out))
	if err != nil {
		if text == "" {
			text = err.Error()
		}
		return text, fmt.Errorf("%s %s: %s: %w", command, strings.Join(args, " "), text, err)
	}
	return text, nil
}

func nestedAndroidNATCommandPath(command string) string {
	switch command {
	case "ip":
		for _, candidate := range []string{"/system/bin/ip", "/usr/sbin/ip", "/sbin/ip"} {
			if info, err := os.Stat(candidate); err == nil && !info.IsDir() {
				return candidate
			}
		}
	}
	return command
}

func parseAndroidDefaultRouteTable(output string) (int, bool) {
	bestPriority := int(^uint(0) >> 1)
	bestTable := 0
	for _, line := range strings.Split(output, "\n") {
		fields := strings.Fields(line)
		priority, table, ok := androidDefaultNetworkPolicyRule(fields)
		if !ok || priority >= bestPriority {
			continue
		}
		bestPriority = priority
		bestTable = table
	}
	return bestTable, bestTable > 0
}

// androidDefaultNetworkPolicyRule accepts only netd's canonical unqualified
// IPv4 default-network rule. Strict matching is intentional: accepting a
// source-, protocol-, or UID-qualified rule could send all nested container
// traffic through a route table owned by another Android component.
func androidDefaultNetworkPolicyRule(fields []string) (int, int, bool) {
	priority, ok := policyRulePriority(fields)
	if !ok {
		return 0, 0, false
	}
	fields = fields[1:]
	if len(fields) >= 2 && fields[0] == "from" && fields[1] == "all" {
		fields = fields[2:]
	}
	if len(fields) != 6 || fields[0] != "fwmark" || !isAndroidDefaultFWMark(fields[1]) || fields[2] != "iif" || fields[3] != "lo" || fields[4] != "lookup" {
		return 0, 0, false
	}
	table, err := strconv.Atoi(fields[5])
	if err != nil || table <= 0 {
		return 0, 0, false
	}
	return priority, table, true
}

func nestedAndroidNATFromRuleTables(output string, scope nestedAndroidNATScope) []int {
	seen := map[int]bool{}
	tables := []int{}
	for _, line := range strings.Split(output, "\n") {
		fields := strings.Fields(line)
		priority, ok := policyRulePriority(fields)
		if !ok || priority != scope.fromSubnetRule {
			continue
		}
		// The exact shape check below has already rejected every foreign
		// selector. Its final token is therefore the only safe lookup table.
		if len(fields) != 5 {
			continue
		}
		tableText := fields[4]
		table, err := strconv.Atoi(tableText)
		if err != nil || table <= 0 || seen[table] || !nestedAndroidNATPolicyRuleMatches(fields, nestedAndroidNATPolicyRule{priority: scope.fromSubnetRule, selector: "from", table: tableText}) {
			continue
		}
		seen[table] = true
		tables = append(tables, table)
	}
	return tables
}

func nestedAndroidNATPolicyRulePresent(output string, rule nestedAndroidNATPolicyRule) bool {
	for _, line := range strings.Split(output, "\n") {
		if nestedAndroidNATPolicyRuleMatches(strings.Fields(line), rule) {
			return true
		}
	}
	return false
}

// nestedAndroidNATPolicyRuleMatches recognizes only the exact canonical rule
// form written by this WebUI. It must stay stricter than the delete command:
// ip rule del accepts a shorter selector and could otherwise remove a host
// rule carrying an additional match condition.
func nestedAndroidNATPolicyRuleMatches(fields []string, rule nestedAndroidNATPolicyRule) bool {
	priority, ok := policyRulePriority(fields)
	if !ok || priority != rule.priority {
		return false
	}
	fields = fields[1:]
	switch rule.selector {
	case "to":
		if len(fields) >= 2 && fields[0] == "from" && fields[1] == "all" {
			fields = fields[2:]
		}
		return len(fields) == 4 && fields[0] == "to" && fields[1] == config.DefaultNATCIDR && fields[2] == "lookup" && fields[3] == rule.table
	case "from":
		return len(fields) == 4 && fields[0] == "from" && fields[1] == config.DefaultNATCIDR && fields[2] == "lookup" && fields[3] == rule.table
	default:
		return false
	}
}

func nestedAndroidNATPolicyRulePriorityUsed(output string, priority int) bool {
	for _, line := range strings.Split(output, "\n") {
		if got, ok := policyRulePriority(strings.Fields(line)); ok && got == priority {
			return true
		}
	}
	return false
}

func policyRulePriority(fields []string) (int, bool) {
	if len(fields) == 0 {
		return 0, false
	}
	priority, err := strconv.Atoi(strings.TrimSuffix(fields[0], ":"))
	return priority, err == nil
}

func isAndroidDefaultFWMark(value string) bool {
	markText, maskText, ok := strings.Cut(value, "/")
	if !ok {
		return false
	}
	mark, markErr := strconv.ParseUint(markText, 0, 32)
	mask, maskErr := strconv.ParseUint(maskText, 0, 32)
	return markErr == nil && maskErr == nil && mark == 0 && mask == 0xffff
}

func hasDefaultIPv4Route(output string) bool {
	for _, line := range strings.Split(output, "\n") {
		fields := strings.Fields(line)
		if len(fields) > 0 && fields[0] == "default" {
			return true
		}
	}
	return false
}

func natCompatNotFound(err error) bool {
	text := strings.ToLower(err.Error())
	return strings.Contains(text, "no such file") || strings.Contains(text, "not found") || strings.Contains(text, "does a matching rule exist")
}

func (s *Server) nestedAndroidNATCompatState() (bool, nestedAndroidNATScope) {
	s.nestedAndroidNATCompatMu.RLock()
	defer s.nestedAndroidNATCompatMu.RUnlock()
	return s.nestedAndroidNATCompat, s.nestedAndroidNATScope
}

func (s *Server) setNestedAndroidNATCompatState(enabled bool, workspacePath string) (bool, nestedAndroidNATScope) {
	s.nestedAndroidNATCompatMu.Lock()
	previousEnabled := s.nestedAndroidNATCompat
	previousScope := s.nestedAndroidNATScope
	s.nestedAndroidNATCompat = enabled
	s.nestedAndroidNATScope = nestedAndroidNATScopeForWorkspace(workspacePath)
	s.nestedAndroidNATCompatMu.Unlock()
	return previousEnabled, previousScope
}

func (s *Server) nestedAndroidNATCompatEnabled() bool {
	enabled, _ := s.nestedAndroidNATCompatState()
	return enabled
}

func (s *Server) reconcileNestedAndroidNATCompatAsync() {
	if s.disableNATCompatRuntime {
		return
	}
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 12*time.Second)
		defer cancel()
		if err := s.reconcileNestedAndroidNATCompat(ctx); err != nil {
			s.recordBackendDiagnostic("nat-compat", err)
			log.Printf("nested Android NAT compatibility: %v", err)
		}
	}()
}

func (s *Server) startNestedAndroidNATCompatMonitor() {
	if s.disableNATCompatRuntime || !s.nestedAndroidNATCompatEnabled() {
		return
	}
	s.nestedAndroidNATCompatMonitorMu.Lock()
	if s.nestedAndroidNATCompatMonitorCancel != nil {
		s.nestedAndroidNATCompatMonitorMu.Unlock()
		return
	}
	ctx, cancel := context.WithCancel(context.Background())
	s.nestedAndroidNATCompatMonitorCancel = cancel
	s.nestedAndroidNATCompatMonitorMu.Unlock()

	go func() {
		s.reconcileNestedAndroidNATCompatAsync()
		ticker := time.NewTicker(20 * time.Second)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				s.reconcileNestedAndroidNATCompatAsync()
			}
		}
	}()
}

func (s *Server) stopNestedAndroidNATCompatMonitor() {
	s.nestedAndroidNATCompatMonitorMu.Lock()
	cancel := s.nestedAndroidNATCompatMonitorCancel
	s.nestedAndroidNATCompatMonitorCancel = nil
	s.nestedAndroidNATCompatMonitorMu.Unlock()
	if cancel != nil {
		cancel()
	}
}

// Close stops WebUI background monitors. Compatibility rules are shared by the
// network namespace, runtime-only, and deliberately left in place so closing
// one WebUI process cannot interrupt another workspace using the same CIDR.
func (s *Server) Close(ctx context.Context) error {
	_, scope := s.nestedAndroidNATCompatState()
	s.setNestedAndroidNATCompatState(false, scope.workspace)
	s.stopNestedAndroidNATCompatMonitor()
	s.stopRootfsCatalogRefreshScheduler()
	s.stopBatteryStatsSampler()
	return nil
}
