package analyzer

import "testing"

func newTestWorkspace(t testing.TB) *Workspace {
	t.Helper()
	ws := NewWorkspace()
	t.Cleanup(ws.Close)
	return ws
}
