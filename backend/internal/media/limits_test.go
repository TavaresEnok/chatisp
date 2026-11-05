package media

import (
	"os/exec"
	"strings"
	"testing"
)

// The child really inherits the ceilings: ask a shell what it was given.
func TestFFmpegChildInheritsResourceLimits(t *testing.T) {
	if _, err := exec.LookPath("prlimit"); err != nil {
		t.Skip("prlimit ausente: o host roda o ffmpeg só com tempo, duração e tamanho")
	}
	name, argv := limitedCommand("/bin/sh", []string{"-c", "ulimit -t; ulimit -v; ulimit -f; ulimit -n"})
	out, err := exec.Command(name, argv...).Output()
	if err != nil {
		t.Fatal(err)
	}
	got := strings.Fields(string(out))
	// /bin/sh reports -v in KiB and -f in 512-byte blocks.
	want := []string{"30", "2097152", "32768", "64"}
	if len(got) != 4 || strings.Join(got, " ") != strings.Join(want, " ") {
		t.Fatalf("limites do filho: %v, esperado %v", got, want)
	}
}
