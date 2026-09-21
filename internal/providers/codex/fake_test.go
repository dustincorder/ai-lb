package codex

// Cross-platform fake `codex` executable for tests, using the
// test-helper-process pattern: the current test binary re-execs itself
// with AI_LB_FAKE_CODEX=1 and behaves like a scripted app-server. No
// shell scripts, so the Windows matrix keeps working.
//
// Behavior is driven by environment:
//   - argv "--version"                        → prints a fake version
//   - argv "app-server"                       → NDJSON stdio server
//   - AI_LB_FAKE_ACCOUNT=json                 → account/read result object (else disconnected)
//   - AI_LB_FAKE_RATELIMITS=json              → rateLimits/read result (else default snapshot)
//   - AI_LB_FAKE_LOGIN=ok|fail                → emit account/login/completed after start
//   - AI_LB_FAKE_EXIT_AFTER=n                 → exit after n input lines
//   - AI_LB_FAKE_BADLINE=1                    → emit one garbage line at startup
//   - AI_LB_FAKE_SILENT=1                     → never answer (timeout/cancel paths)

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"strconv"
	"strings"
	"testing"
	"time"
)

func TestMain(m *testing.M) {
	if os.Getenv("AI_LB_FAKE_CODEX") == "1" {
		os.Exit(fakeMain())
	}
	os.Exit(m.Run())
}

func fakeMain() int {
	args := os.Args[1:]
	if len(args) == 1 && args[0] == "--version" {
		fmt.Println("codex-cli 0.0.0-fake")
		return 0
	}
	if len(args) == 1 && args[0] == "app-server" {
		return fakeAppServer()
	}
	fmt.Fprintln(os.Stderr, "fake codex: unknown args")
	return 2
}

func fakeWrite(v any) {
	data, _ := json.Marshal(v)
	fmt.Println(string(data))
}

func fakeAppServer() int {
	if os.Getenv("AI_LB_FAKE_BADLINE") == "1" {
		fmt.Println("this is not json {{{")
	}
	sc := bufio.NewScanner(os.Stdin)
	sc.Buffer(make([]byte, 64*1024), 1<<20)
	lines := 0
	silent := os.Getenv("AI_LB_FAKE_SILENT") == "1"
	quietAfterInit := os.Getenv("AI_LB_FAKE_QUIET_AFTER_INIT") == "1"
	initialized := false
	exitAfter := -1
	if n, err := strconv.Atoi(os.Getenv("AI_LB_FAKE_EXIT_AFTER")); err == nil {
		exitAfter = n
	}
	for sc.Scan() {
		lines++
		if exitAfter >= 0 && lines > exitAfter {
			return 1
		}
		var msg struct {
			ID     any             `json:"id"`
			Method string          `json:"method"`
			Params json.RawMessage `json:"params"`
		}
		if err := json.Unmarshal(sc.Bytes(), &msg); err != nil {
			continue
		}
		if silent {
			continue
		}
		if quietAfterInit && initialized && msg.Method != "initialize" && msg.Method != "initialized" {
			continue
		}
		switch msg.Method {
		case "initialize":
			initialized = true
			fakeWrite(map[string]any{"id": msg.ID, "result": map[string]any{
				"userAgent": "fake/0.0.0", "codexHome": os.Getenv("CODEX_HOME"),
			}})
		case "initialized":
			// notification: no reply
		case "account/read":
			account := any(nil)
			if raw := os.Getenv("AI_LB_FAKE_ACCOUNT"); raw != "" {
				var a any
				_ = json.Unmarshal([]byte(raw), &a)
				account = a
			}
			fakeWrite(map[string]any{"id": msg.ID, "result": map[string]any{
				"account": account, "requiresOpenaiAuth": true,
			}})
		case "account/login/start":
			var p struct {
				Type string `json:"type"`
			}
			_ = json.Unmarshal(msg.Params, &p)
			loginID := "login-" + p.Type + "-1"
			if p.Type == "chatgptDeviceCode" {
				fakeWrite(map[string]any{"id": msg.ID, "result": map[string]any{
					"type": p.Type, "loginId": loginID,
					"verificationUrl": "https://example.com/device", "userCode": "ABCD-1234",
				}})
			} else {
				fakeWrite(map[string]any{"id": msg.ID, "result": map[string]any{
					"type": p.Type, "loginId": loginID,
					"authUrl": "https://example.com/auth",
				}})
			}
			if mode := os.Getenv("AI_LB_FAKE_LOGIN"); mode != "" {
				go func() {
					time.Sleep(200 * time.Millisecond)
					params := map[string]any{"loginId": loginID, "success": mode == "ok"}
					if mode != "ok" {
						params["error"] = "user cancelled at provider"
					}
					fakeWrite(map[string]any{
						"method": "account/login/completed", "params": params,
					})
				}()
			}
		case "account/login/cancel":
			fakeWrite(map[string]any{"id": msg.ID, "result": map[string]any{"status": "cancelled"}})
		case "account/logout":
			fakeWrite(map[string]any{"id": msg.ID, "result": map[string]any{}})
		case "account/rateLimits/read":
			if raw := os.Getenv("AI_LB_FAKE_RATELIMITS"); strings.HasPrefix(raw, "ERROR:") {
				rest := strings.TrimPrefix(raw, "ERROR:")
				code := -32600
				errMsg := rest
				if i := strings.Index(rest, ":"); i >= 0 {
					if n, err := strconv.Atoi(rest[:i]); err == nil {
						code = n
					}
					errMsg = rest[i+1:]
				}
				fakeWrite(map[string]any{"id": msg.ID, "error": map[string]any{
					"code": code, "message": errMsg,
				}})
			} else if raw := os.Getenv("AI_LB_FAKE_RATELIMITS"); raw != "" {
				var r any
				_ = json.Unmarshal([]byte(raw), &r)
				fakeWrite(map[string]any{"id": msg.ID, "result": r})
			} else {
				fakeWrite(map[string]any{"id": msg.ID, "result": map[string]any{
					"rateLimits": map[string]any{
						"limitId": "codex", "limitName": "Codex",
						"primary": map[string]any{
							"usedPercent": 30, "windowDurationMins": 300,
							"resetsAt": time.Now().Add(time.Hour).Unix(),
						},
					},
				}})
			}
		default:
			fakeWrite(map[string]any{"id": msg.ID, "error": map[string]any{
				"code": -32601, "message": "fake: unknown method " + msg.Method,
			}})
		}
	}
	return 0
}

// fakeEnv runs the current test binary as a fake codex with extra env.
func fakeEnv(t *testing.T, extra ...string) (string, []string) {
	t.Helper()
	exe, err := os.Executable()
	if err != nil {
		t.Fatalf("os.Executable: %v", err)
	}
	env := append(os.Environ(), "AI_LB_FAKE_CODEX=1")
	return exe, append(env, extra...)
}
