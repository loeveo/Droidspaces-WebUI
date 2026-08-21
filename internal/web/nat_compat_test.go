package web

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

func TestParseAndroidDefaultRouteTable(t *testing.T) {
	output := `0: from all lookup local
10000: from all fwmark 0/0xffff iif lo lookup 1023
11000: from all fwmark 0x0/0xffff iif lo uidrange 10000-19999 lookup 1017
32766: from all lookup main`
	if got, ok := parseAndroidDefaultRouteTable(output); !ok || got != 1023 {
		t.Fatalf("parseAndroidDefaultRouteTable() = %d, %v; want 1023, true", got, ok)
	}

	// The earliest global default-network rule wins, while uid-scoped rules
	// must be ignored even when they have a smaller priority.
	output = `9000: from all fwmark 0x0/0xffff iif lo uidrange 1000-2000 lookup 77
10010: from all fwmark 0x0/0xffff iif lo lookup 1017
10020: from all fwmark 0x0/0xffff iif lo lookup 1023`
	if got, ok := parseAndroidDefaultRouteTable(output); !ok || got != 1017 {
		t.Fatalf("parseAndroidDefaultRouteTable() = %d, %v; want 1017, true", got, ok)
	}

	if _, ok := parseAndroidDefaultRouteTable("10000: from all fwmark 0x1/0xffff iif lo lookup 1023"); ok {
		t.Fatal("non-default fwmark was accepted")
	}
	if _, ok := parseAndroidDefaultRouteTable("10000: from all to 172.28.0.0/16 fwmark 0/0xffff iif lo lookup 1023"); ok {
		t.Fatal("destination-scoped Android rule was accepted")
	}
	if _, ok := parseAndroidDefaultRouteTable("10000: from all fwmark 0/0xffff iif lo tos 0x10 lookup 1023"); ok {
		t.Fatal("selector-qualified Android rule was accepted")
	}
	if _, ok := parseAndroidDefaultRouteTable("10000: from all fwmark 0/0xffff iif lo lookup 1023 proto babel"); ok {
		t.Fatal("protocol-qualified Android rule was accepted")
	}
}

func TestNestedAndroidNATPolicyRulesAreScoped(t *testing.T) {
	srv, workspace, _ := newTestServer(t)
	srv.setNestedAndroidNATCompatState(true, workspace)
	scope := nestedAndroidNATScopeForWorkspace(workspace)
	makeLiveNATContainer(t, workspace, "nested-nat")

	var calls []string
	rules := strings.Join([]string{
		"0: from all lookup local",
		"31000: from all fwmark 0/0xffff iif lo lookup 1023",
		"32766: from all lookup main",
	}, "\n")
	srv.nestedAndroidNATCommand = func(_ context.Context, command string, args ...string) (string, error) {
		call := command + " " + strings.Join(args, " ")
		calls = append(calls, call)
		if command != "ip" {
			return "", fmt.Errorf("unexpected command %q", command)
		}
		switch strings.Join(args, " ") {
		case "-4 rule show":
			return rules, nil
		case "-4 route show table 1023":
			return "default via 192.168.4.1 dev wlan0", nil
		}
		if len(args) >= 4 && args[0] == "-4" && args[1] == "rule" && args[2] == "add" {
			priority := args[4]
			selector := args[5]
			table := args[len(args)-1]
			if selector == "to" {
				rules += "\n" + priority + ": from all to 172.28.0.0/16 lookup " + table
			} else {
				rules += "\n" + priority + ": from 172.28.0.0/16 lookup " + table
			}
			return "", nil
		}
		return "", fmt.Errorf("unexpected ip command %q", strings.Join(args, " "))
	}

	if err := srv.reconcileNestedAndroidNATCompat(context.Background()); err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		fmt.Sprintf("ip -4 rule add pref %d to 172.28.0.0/16 lookup main", scope.toSubnetRule),
		fmt.Sprintf("ip -4 rule add pref %d from 172.28.0.0/16 lookup 97", scope.tetherRule),
		fmt.Sprintf("ip -4 rule add pref %d from 172.28.0.0/16 lookup 1023", scope.fromSubnetRule),
	} {
		if !containsString(calls, want) {
			t.Fatalf("expected command %q; calls:\n%s", want, strings.Join(calls, "\n"))
		}
	}
	for _, call := range calls {
		if strings.Contains(call, "iptables") || strings.Contains(call, " 6090") || strings.Contains(call, " 6095") || strings.Contains(call, " 6100") || strings.Contains(call, "MASQUERADE") || strings.Contains(call, "-t nat") || strings.Contains(call, " INPUT ") || strings.Contains(call, " DNAT") || strings.Contains(call, " POSTROUTING") {
			t.Fatalf("compatibility layer touched non-WebUI networking state: %s", call)
		}
	}
}

func TestNestedAndroidNATPolicyRuleConflictDoesNotOverwrite(t *testing.T) {
	srv, workspace, _ := newTestServer(t)
	srv.setNestedAndroidNATCompatState(true, workspace)
	scope := nestedAndroidNATScopeForWorkspace(workspace)
	makeLiveNATContainer(t, workspace, "nested-nat")

	var calls []string
	srv.nestedAndroidNATCommand = func(_ context.Context, command string, args ...string) (string, error) {
		calls = append(calls, command+" "+strings.Join(args, " "))
		if command == "ip" && strings.Join(args, " ") == "-4 rule show" {
			return fmt.Sprintf("%d: from all lookup 101\n31000: from all fwmark 0/0xffff iif lo lookup 1023", scope.toSubnetRule), nil
		}
		if command == "ip" && strings.Join(args, " ") == "-4 route show table 1023" {
			return "default via 192.168.4.1 dev wlan0", nil
		}
		return "", nil
	}

	err := srv.reconcileNestedAndroidNATCompat(context.Background())
	if err == nil || !strings.Contains(err.Error(), strconv.Itoa(scope.toSubnetRule)) {
		t.Fatalf("conflicting priority error = %v", err)
	}
	for _, call := range calls {
		if strings.Contains(call, "ip -4 rule add") || strings.Contains(call, "ip -4 rule del") {
			t.Fatalf("conflicting rule was modified: %s", call)
		}
	}
}

func TestNestedAndroidNATPolicyRuleConflictWithProtocolDoesNotOverwrite(t *testing.T) {
	srv, workspace, _ := newTestServer(t)
	srv.setNestedAndroidNATCompatState(true, workspace)
	scope := nestedAndroidNATScopeForWorkspace(workspace)
	makeLiveNATContainer(t, workspace, "nested-nat")

	var calls []string
	srv.nestedAndroidNATCommand = func(_ context.Context, command string, args ...string) (string, error) {
		call := command + " " + strings.Join(args, " ")
		calls = append(calls, call)
		switch strings.Join(args, " ") {
		case "-4 rule show":
			return strings.Join([]string{
				fmt.Sprintf("%d: from 172.28.0.0/16 lookup 1023 proto babel", scope.fromSubnetRule),
				"31000: from all fwmark 0/0xffff iif lo lookup 1023",
			}, "\n"), nil
		case "-4 route show table 1023":
			return "default via 192.168.4.1 dev wlan0", nil
		}
		return "", nil
	}

	err := srv.reconcileNestedAndroidNATCompat(context.Background())
	if err == nil || !strings.Contains(err.Error(), strconv.Itoa(scope.fromSubnetRule)) {
		t.Fatalf("protocol-qualified conflict error = %v", err)
	}
	for _, call := range calls {
		if strings.Contains(call, "ip -4 rule add") || strings.Contains(call, "ip -4 rule del") {
			t.Fatalf("protocol-qualified foreign rule was modified: %s", call)
		}
	}
}

func TestNestedAndroidNATRemovesOnlyStaleSharedUplinkRule(t *testing.T) {
	srv, workspace, _ := newTestServer(t)
	srv.setNestedAndroidNATCompatState(true, workspace)
	scope := nestedAndroidNATScopeForWorkspace(workspace)
	makeLiveNATContainer(t, workspace, "nested-nat")
	var calls []string
	rules := strings.Join([]string{
		"6090: to 172.28.0.0/16 lookup main",
		fmt.Sprintf("%d: from all to 172.28.0.0/16 lookup main", scope.toSubnetRule),
		fmt.Sprintf("%d: from 172.28.0.0/16 lookup 97", scope.tetherRule),
		fmt.Sprintf("%d: from 172.28.0.0/16 lookup 1017", scope.fromSubnetRule),
		fmt.Sprintf("%d: from 172.28.0.0/16 to 10.0.0.0/8 lookup 99", scope.fromSubnetRule+1),
		"6100: from 172.28.0.0/16 lookup 1023",
		"31000: from all fwmark 0/0xffff iif lo lookup 1023",
	}, "\n")
	srv.nestedAndroidNATCommand = func(_ context.Context, command string, args ...string) (string, error) {
		call := command + " " + strings.Join(args, " ")
		calls = append(calls, call)
		if command == "ip" && strings.Join(args, " ") == "-4 rule show" {
			return rules, nil
		}
		if command == "ip" && strings.Join(args, " ") == "-4 route show table 1023" {
			return "default via 192.168.4.1 dev wlan0", nil
		}
		if command == "ip" && len(args) >= 8 && strings.Join(args[:3], " ") == "-4 rule del" {
			priority := args[4]
			table := args[len(args)-1]
			needle := priority + ": from 172.28.0.0/16 lookup " + table
			rules = strings.Join(filterLines(rules, needle), "\n")
			return "", nil
		}
		if command == "ip" && len(args) >= 8 && strings.Join(args[:3], " ") == "-4 rule add" {
			priority := args[4]
			selector := args[5]
			table := args[len(args)-1]
			if selector == "to" {
				rules += "\n" + priority + ": from all to 172.28.0.0/16 lookup " + table
			} else {
				rules += "\n" + priority + ": from 172.28.0.0/16 lookup " + table
			}
			return "", nil
		}
		return "", nil
	}

	if err := srv.reconcileNestedAndroidNATCompat(context.Background()); err != nil {
		t.Fatal(err)
	}
	want := fmt.Sprintf("ip -4 rule del pref %d from 172.28.0.0/16 lookup 1017", scope.fromSubnetRule)
	if !containsString(calls, want) {
		t.Fatalf("expected stale uplink cleanup %q; calls:\n%s", want, strings.Join(calls, "\n"))
	}
	for _, call := range calls {
		if strings.Contains(call, "iptables") || strings.Contains(call, "6090") || strings.Contains(call, "6100") || strings.Contains(call, "lookup 99") || strings.Contains(call, "lookup main") || strings.Contains(call, "lookup 97") {
			t.Fatalf("cleanup touched non-WebUI state: %s", call)
		}
	}
}

func TestNestedAndroidNATPolicyRuleMatchingUsesRealIPOutput(t *testing.T) {
	scope := nestedAndroidNATScopeForWorkspace("/var/lib/Droidspaces")
	toRule := nestedAndroidNATPolicyRule{priority: scope.toSubnetRule, selector: "to", table: "main"}
	fromRule := nestedAndroidNATPolicyRule{priority: scope.fromSubnetRule, selector: "from", table: "1023"}
	if !nestedAndroidNATPolicyRulePresent(fmt.Sprintf("%d: from all to 172.28.0.0/16 lookup main", scope.toSubnetRule), toRule) {
		t.Fatal("destination rule with ip's implicit 'from all' was not recognized")
	}
	if !nestedAndroidNATPolicyRulePresent(fmt.Sprintf("%d: from 172.28.0.0/16 lookup 1023", scope.fromSubnetRule), fromRule) {
		t.Fatal("source rule was not recognized")
	}
	for _, line := range []string{
		fmt.Sprintf("%d: from 10.0.0.0/8 to 172.28.0.0/16 lookup main", scope.toSubnetRule),
		fmt.Sprintf("%d: from all to 172.28.0.0/16 fwmark 0x1/0xffff lookup main", scope.toSubnetRule),
		fmt.Sprintf("%d: from 172.28.0.0/16 iif lo lookup 1023", scope.fromSubnetRule),
		fmt.Sprintf("%d: from 172.28.0.0/16 tos 0x10 lookup 1023", scope.fromSubnetRule),
		fmt.Sprintf("%d: from 172.28.0.0/16 suppress_prefixlength 0 lookup 1023", scope.fromSubnetRule),
		fmt.Sprintf("%d: from 172.28.0.0/16 lookup 1023 proto babel", scope.fromSubnetRule),
	} {
		if nestedAndroidNATPolicyRulePresent(line, toRule) || nestedAndroidNATPolicyRulePresent(line, fromRule) {
			t.Fatalf("non-WebUI rule was recognized as owned: %q", line)
		}
	}
}

func TestNestedAndroidNATScopeIsSharedAcrossWorkspaces(t *testing.T) {
	first := nestedAndroidNATScopeForWorkspace("/var/lib/Droidspaces")
	second := nestedAndroidNATScopeForWorkspace("/var/lib/Droidspaces-alt")
	if first.toSubnetRule != second.toSubnetRule || first.tetherRule != second.tetherRule || first.fromSubnetRule != second.fromSubnetRule {
		t.Fatalf("shared NAT CIDR must use one route scope: %#v %#v", first, second)
	}
}

func TestNestedAndroidNATSharedRulesPrecedeLegacyPreReleaseRange(t *testing.T) {
	scope := nestedAndroidNATScopeForWorkspace("/var/lib/Droidspaces")
	if scope.fromSubnetRule >= 7000 || scope.toSubnetRule <= 6100 {
		t.Fatalf("compatibility priorities must precede legacy 7000+ rules without colliding with core: %#v", scope)
	}
}

func filterLines(text string, omit string) []string {
	var result []string
	for _, line := range strings.Split(text, "\n") {
		if line != omit {
			result = append(result, line)
		}
	}
	return result
}

func makeLiveNATContainer(t *testing.T, workspacePath string, name string) {
	t.Helper()
	containerDir := filepath.Join(workspacePath, "Containers", name)
	if err := os.MkdirAll(filepath.Join(workspacePath, "Pids"), 0755); err != nil {
		t.Fatal(err)
	}
	mustWriteFile(t, filepath.Join(containerDir, "container.config"), []byte("name="+name+"\nnet_mode=nat\n"), 0644)
	mustWriteFile(t, filepath.Join(workspacePath, "Pids", name+".pid"), []byte(strconv.Itoa(os.Getpid())+"\n"), 0644)
}

func containsString(values []string, want string) bool {
	for _, value := range values {
		if value == want {
			return true
		}
	}
	return false
}
