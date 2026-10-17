package scripts_test

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// 所有 Git/Go 工具都替换为只记录参数的桩；测试绝不创建真实标签、提交或联网推送。
func TestReleaseGuardsAndAtomicPublication(t *testing.T) {
	source, err := os.ReadFile("release.sh")
	if err != nil {
		t.Fatal(err)
	}
	for _, test := range []struct {
		name, mode, variable, message string
		publish, invalidVersion       bool
	}{
		{name: "dirty tree", mode: "publish", variable: "RELEASE_TEST_DIRTY=1", message: "请先提交全部修复"},
		{name: "missing changelog", mode: "check", invalidVersion: true, message: "对应发布记录"},
		{name: "failed tests", mode: "publish", variable: "RELEASE_TEST_FAIL=1", message: "mock test failure"},
		{name: "head changed", mode: "publish", variable: "RELEASE_TEST_DRIFT=1", message: "检查期间工作区或提交发生变化"},
		{name: "check does not publish", mode: "check"},
		{name: "publish full revision", mode: "publish", publish: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			dir := t.TempDir()
			bin := filepath.Join(dir, "bin")
			if err := os.MkdirAll(filepath.Join(dir, "scripts"), 0755); err != nil {
				t.Fatal(err)
			}
			if err := os.Mkdir(bin, 0755); err != nil {
				t.Fatal(err)
			}
			changelog := "## v1.2.3\n"
			if test.invalidVersion {
				changelog = "## other\n"
			}
			files := map[string]string{
				"scripts/release.sh": string(source),
				"version.go":         "package fixture\nconst Version = \"v1.2.3\"\n",
				"CHANGELOG.md":       changelog,
				"bin/goctl":          "#!/bin/bash\nexit 0\n",
				"bin/git": `#!/bin/bash
set -eu
printf 'git %s\n' "$*" >> "$RELEASE_TEST_LOG"
case "$1" in
 status) if [[ "${RELEASE_TEST_DIRTY:-}" == 1 ]]; then echo ' M model.go'; fi ;;
 show-ref) exit 1 ;;
 ls-remote) exit 0 ;;
 symbolic-ref) echo main ;;
 rev-parse)
   if [[ "${RELEASE_TEST_DRIFT:-}" == 1 && -f "$RELEASE_TEST_STATE" ]]; then
     echo second_revision
   else
     touch "$RELEASE_TEST_STATE"
     echo tested_revision
   fi ;;
 diff|tag|push) exit 0 ;;
 *) echo "unexpected git operation: $*" >&2; exit 2 ;;
esac
`,
				"bin/go": `#!/bin/bash
set -eu
printf 'go %s\n' "$*" >> "$RELEASE_TEST_LOG"
if [[ "$1" == test && "${RELEASE_TEST_FAIL:-}" == 1 ]]; then
 echo 'mock test failure' >&2
 exit 1
fi
`,
			}
			for name, data := range files {
				if err := os.WriteFile(filepath.Join(dir, name), []byte(data), 0755); err != nil {
					t.Fatal(err)
				}
			}
			logPath := filepath.Join(dir, "commands.log")
			command := exec.Command("bash", filepath.Join(dir, "scripts/release.sh"), test.mode)
			command.Env = append(os.Environ(), "PATH="+bin+string(os.PathListSeparator)+os.Getenv("PATH"),
				"MONGOPLUS_TEST_URI=mongodb://test-only.invalid", "RELEASE_TEST_LOG="+logPath, "RELEASE_TEST_STATE="+filepath.Join(dir, "head.checked"))
			if test.variable != "" {
				command.Env = append(command.Env, test.variable)
			}
			output, runErr := command.CombinedOutput()
			if test.message != "" {
				if runErr == nil || !strings.Contains(string(output), test.message) {
					t.Fatalf("未拒绝无效发布: %v\n%s", runErr, output)
				}
			} else if runErr != nil {
				t.Fatalf("执行失败: %v\n%s", runErr, output)
			}
			data, _ := os.ReadFile(logPath)
			log := string(data)
			if test.publish {
				if !strings.Contains(log, "git tag -a v1.2.3 tested_revision") || !strings.Contains(log, "git push --atomic origin HEAD:refs/heads/main refs/tags/v1.2.3") {
					t.Fatalf("未原子发布被测试的完整提交: %s", log)
				}
			} else if strings.Contains(log, "git tag ") || strings.Contains(log, "git push ") {
				t.Fatalf("失败或只检查时创建了标签/推送: %s", log)
			}
			if strings.Contains(log, "git add ") || strings.Contains(log, "git commit ") {
				t.Fatal("发布脚本不得自动提交部分工作树")
			}
			if test.name == "dirty tree" && (strings.Contains(log, "git ls-remote") || strings.Contains(log, "go ")) {
				t.Fatal("应在联网和测试之前拒绝脏工作区")
			}
		})
	}
}
