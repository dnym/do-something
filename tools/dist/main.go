// Command dist produces reproducible pure-Go release binaries and SHA256SUMS.
package main

import (
	"crypto/sha256"
	"flag"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
)

func main() {
	version := flag.String("version", "0.1.0-dev", "release version")
	flag.Parse()
	if e := run(*version); e != nil {
		fmt.Fprintln(os.Stderr, e)
		os.Exit(1)
	}
}
func run(version string) error {
	if e := os.MkdirAll("dist", 0755); e != nil {
		return e
	}
	sums := []string{}
	for _, target := range []struct{ os, arch string }{{"linux", "amd64"}, {"linux", "arm64"}, {"darwin", "amd64"}, {"darwin", "arm64"}, {"windows", "amd64"}, {"windows", "arm64"}} {
		name := "do-something-" + target.os + "-" + target.arch
		if target.os == "windows" {
			name += ".exe"
		}
		path := filepath.Join("dist", name)
		cmd := exec.Command("go", "build", "-trimpath", "-buildvcs=false", "-ldflags", "-s -w -X dosomething/internal/cli.Version="+version, "-o", path, "./cmd/dosomething")
		cmd.Env = append(os.Environ(), "CGO_ENABLED=0", "GOOS="+target.os, "GOARCH="+target.arch)
		cmd.Stdout = os.Stdout
		cmd.Stderr = os.Stderr
		fmt.Fprintln(os.Stderr, name)
		if e := cmd.Run(); e != nil {
			return e
		}
		b, e := os.ReadFile(path)
		if e != nil {
			return e
		}
		sums = append(sums, fmt.Sprintf("%x  %s", sha256.Sum256(b), name))
	}
	sort.Strings(sums)
	return os.WriteFile(filepath.Join("dist", "SHA256SUMS"), []byte(strings.Join(sums, "\n")+"\n"), 0644)
}
