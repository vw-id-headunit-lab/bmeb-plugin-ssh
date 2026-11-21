package sshplugin

import (
	"encoding/json"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"
)

// Helpers run as subprocesses: no real firewall or host sshd is modified.
func TestSSHPluginHelper(t *testing.T) {
	if os.Getenv("BETTERMEB_SSH_TEST_HELPER") != "1" {
		return
	}
	args := os.Args
	for len(args) > 0 && args[0] != "--" {
		args = args[1:]
	}
	args = args[1:]
	if args[0] == "iptables" {
		args = args[1:]
		if len(args) > 1 && args[0] == "-w" {
			args = args[2:]
		}
		op := args[0]
		if args[1] != "INPUT" {
			os.Exit(10)
		}
		ruleArgs := args[2:]
		if op == "-I" {
			ruleArgs = ruleArgs[1:]
		}
		if strings.Join(ruleArgs, " ") != "-p tcp --dport 2223 -m comment --comment bettermeb-sshd-2223 -j ACCEPT" {
			os.Exit(11)
		}
		if _, err := os.Stat(os.Getenv("SSH_TEST_QUERY_ERROR")); err == nil {
			os.Exit(2)
		}
		path := os.Getenv("SSH_TEST_RULES")
		raw, _ := os.ReadFile(path)
		has := strings.Contains(string(raw), "plugin-2223\n")
		switch op {
		case "-C":
			if !has {
				os.Exit(1)
			}
		case "-I":
			_ = os.WriteFile(path, append(raw, []byte("plugin-2223\n")...), 0o600)
		case "-D":
			_ = os.WriteFile(path, []byte(strings.Replace(string(raw), "plugin-2223\n", "", 1)), 0o600)
		default:
			os.Exit(12)
		}
		os.Exit(0)
	}
	args = args[1:]
	raw, _ := json.Marshal(args)
	_ = os.WriteFile(os.Getenv("SSH_TEST_ARGS"), raw, 0o600)
	if len(args) > 0 && args[0] == "-t" {
		os.Exit(0)
	}
	listener, err := net.Listen("tcp4", "127.0.0.1:2223")
	if err != nil {
		os.Exit(1)
	}
	defer listener.Close()
	for {
		conn, err := listener.Accept()
		if err != nil {
			os.Exit(1)
		}
		_ = conn.Close()
	}
}

func TestSSHPluginIsolatedLifecycle(t *testing.T) {
	if _, err := exec.LookPath("ssh-keygen"); err != nil {
		t.Skip("ssh-keygen unavailable")
	}
	probe, err := net.Listen("tcp4", "127.0.0.1:2223")
	if err != nil {
		t.Skip("2223 already in use in test environment")
	}
	probe.Close()
	root := t.TempDir()
	bin, data, runtime := filepath.Join(root, "bin"), filepath.Join(root, "data"), filepath.Join(root, "runtime")
	for _, dir := range []string{bin, data, runtime} {
		if err := os.MkdirAll(dir, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	pluginDir, _ := filepath.Abs(".")
	rules, errorFlag, argsPath := filepath.Join(root, "rules"), filepath.Join(root, "query-error"), filepath.Join(root, "args")
	if err := os.WriteFile(rules, []byte("system-2222\nunrelated-2223\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"sshd", "iptables"} {
		wrapper := "#!/bin/sh\nexec '" + strings.ReplaceAll(os.Args[0], "'", "'\\''") + "' -test.run=^TestSSHPluginHelper$ -- " + name + " \"$@\"\n"
		if err := os.WriteFile(filepath.Join(bin, name), []byte(wrapper), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	env := append(os.Environ(), "BETTERMEB_SSH_TEST_HELPER=1", "PATH="+bin+":"+os.Getenv("PATH"), "PLUGIN_DIR="+pluginDir,
		"PLUGIN_DATA_DIR="+data, "PLUGIN_TMP_DIR="+runtime, "PLUGIN_GENERATION=1", "SSH_TEST_RULES="+rules, "SSH_TEST_QUERY_ERROR="+errorFlag, "SSH_TEST_ARGS="+argsPath)
	waitFile := func(name string) {
		t.Helper()
		deadline := time.Now().Add(5 * time.Second)
		for time.Now().Before(deadline) {
			if raw, _ := os.ReadFile(name); len(raw) > 0 {
				return
			}
			time.Sleep(10 * time.Millisecond)
		}
		t.Fatalf("missing checkpoint %s", name)
	}
	var key []byte
	for i := 0; i < 2; i++ {
		if i == 1 {
			if err := os.WriteFile(filepath.Join(data, "authorized_keys"), []byte("fixture-user-public-key\n"), 0o600); err != nil {
				t.Fatal(err)
			}
		}
		cmd := exec.Command("/bin/sh", filepath.Join(pluginDir, "run.sh"))
		cmd.Env = env
		cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
		log, err := os.Create(filepath.Join(root, "run.log"))
		if err != nil {
			t.Fatal(err)
		}
		cmd.Stdout = log
		cmd.Stderr = log
		if err := cmd.Start(); err != nil {
			t.Fatal(err)
		}
		done := make(chan error, 1)
		go func() { done <- cmd.Wait() }()
		stop := func() {
			_ = syscall.Kill(-cmd.Process.Pid, syscall.SIGTERM)
			select {
			case <-done:
			case <-time.After(3 * time.Second):
				_ = syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
				<-done
				t.Error("plugin did not stop")
			}
			log.Close()
		}
		func() {
			defer stop()
			waitFile(filepath.Join(runtime, "ready"))
			current, err := os.ReadFile(filepath.Join(data, "ssh_host_ed25519_key"))
			if err != nil {
				t.Fatal(err)
			}
			if i == 0 {
				key = current
			} else if string(key) != string(current) {
				t.Fatal("host key regenerated")
			}
			raw, _ := os.ReadFile(argsPath)
			var args []string
			if err := json.Unmarshal(raw, &args); err != nil {
				t.Fatal(err)
			}
			joined := strings.Join(args, " ")
			for _, expected := range []string{"-f /dev/null", "-p 2223", "-h " + filepath.Join(data, "ssh_host_ed25519_key"), "PidFile=none", "AuthorizedKeysFile=" + filepath.Join(data, "authorized_keys"), "PermitRootLogin=yes", "PasswordAuthentication=yes", "PermitEmptyPasswords=yes", "UsePAM=no", "AuthenticationMethods=any"} {
				if !strings.Contains(joined, expected) {
					t.Fatalf("missing sshd option %q: %s", expected, joined)
				}
			}
		}()
		raw, _ := os.ReadFile(rules)
		if string(raw) != "system-2222\nunrelated-2223\n" {
			t.Fatalf("host rules changed: %s", raw)
		}
		raw, _ = os.ReadFile(filepath.Join(runtime, "cleanup.status"))
		if strings.TrimSpace(string(raw)) != "CLEAN" {
			t.Fatalf("bad cleanup status: %s", raw)
		}
		if _, err := os.Stat(filepath.Join(runtime, "ready")); !os.IsNotExist(err) {
			t.Fatal("ready file survived stop")
		}
	}
	auth, _ := os.ReadFile(filepath.Join(data, "authorized_keys"))
	if string(auth) != "fixture-user-public-key\n" {
		t.Fatal("user authorization replaced")
	}
	// Port conflict must not pass readiness or terminate the pre-existing listener.
	occupied, err := net.Listen("tcp4", "127.0.0.1:2223")
	if err != nil {
		t.Fatal(err)
	}
	defer occupied.Close()
	cmd := exec.Command("/bin/sh", filepath.Join(pluginDir, "run.sh"))
	cmd.Env = env
	if out, err := cmd.CombinedOutput(); err == nil {
		t.Fatalf("port conflict accepted: %s", out)
	}
	if conn, err := net.Dial("tcp4", "127.0.0.1:2223"); err != nil {
		t.Fatal("existing listener was stopped")
	} else {
		conn.Close()
	}
	// Unknown firewall state must not be reported CLEAN by standalone cleanup.
	if err := os.WriteFile(errorFlag, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	cmd = exec.Command("/bin/sh", filepath.Join(pluginDir, "cleanup.sh"))
	cmd.Env = env
	if err := cmd.Run(); err == nil {
		t.Fatal("firewall query failure reported success")
	}
	raw, _ := os.ReadFile(filepath.Join(runtime, "cleanup.status"))
	if !strings.HasPrefix(string(raw), "FAILED:") {
		t.Fatalf("query error hidden: %s", raw)
	}
}
