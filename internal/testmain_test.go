package internal

import (
	"os"
	"testing"
)

// TestMain lets the existing tests, which build watch and library dirs under
// t.TempDir() or use /data/... literals, pass path confinement; confinement tests override it.
func TestMain(m *testing.M) {
	sep := string(os.PathListSeparator)
	_ = os.Setenv("SCANNER_ALLOWED_ROOTS", os.TempDir()+sep+"/data"+sep+"/new")
	os.Exit(m.Run())
}
