package i18n

import (
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"
)

var call = regexp.MustCompile(`\b(?:T|N|F)\(("(?:[^"\\]|\\.)*")`)

// Every text passed to T, N or F anywhere in the program must have an
// English entry, and format verbs must survive translation.
func TestComplete(t *testing.T) {
	verbs := regexp.MustCompile(`%[a-z]`)
	seen := 0
	err := filepath.WalkDir("../..", func(p string, d os.DirEntry, err error) error {
		if err != nil || d.IsDir() || !strings.HasSuffix(p, ".go") || strings.HasSuffix(p, "_test.go") || strings.Contains(p, "selftest") {
			return err
		}
		src, err := os.ReadFile(p)
		if err != nil {
			return err
		}
		for _, m := range call.FindAllSubmatch(src, -1) {
			key, err := strconv.Unquote(string(m[1]))
			if err != nil || !strings.ContainsFunc(key, func(r rune) bool { return r > 0x2E80 }) {
				continue // not Chinese: nothing to translate
			}
			seen++
			v, ok := en[key]
			if !ok {
				t.Errorf("%s: no English for %q", p, key)
				continue
			}
			if a, b := verbs.FindAllString(key, -1), verbs.FindAllString(v, -1); strings.Join(a, "") != strings.Join(b, "") {
				t.Errorf("%q: format verbs differ: %v and %v", key, a, b)
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if seen < 300 {
		t.Fatalf("only %d texts found; the scan is broken", seen)
	}
}

func TestSwitch(t *testing.T) {
	Set("en")
	if T("保存") != "Save" || F("已运行 %d 次", 3) != "Ran 3 times" || T("没有这条") != "没有这条" {
		t.Fatal("English lookup")
	}
	Set("zh")
	if T("保存") != "保存" || Lang() != "zh" {
		t.Fatal("Chinese is the source")
	}
}
