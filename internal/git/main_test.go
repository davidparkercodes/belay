package git

import (
	"fmt"
	"os"
	"testing"

	"github.com/davidparkercodes/belay/internal/testenv"
)

func TestMain(m *testing.M) {
	cleanup, err := testenv.IsolateGit()
	if err != nil {
		fmt.Fprintln(os.Stderr, "isolate git:", err)
		os.Exit(1)
	}
	code := m.Run()
	cleanup()
	os.Exit(code)
}
