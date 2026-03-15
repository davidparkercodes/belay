//go:build darwin

package watcher

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/davidparkercodes/belay/internal/schema"

	"github.com/fsnotify/fsevents"
)

// ─── mapFSEventOp() ─────────────────────────────────────────────────────────

func TestMapFSEventOp_Removed(t *testing.T) {
	got := mapFSEventOp(fsevents.ItemRemoved, "/tmp/file.go")
	if got != schema.OpDelete {
		t.Errorf("ItemRemoved => %v, want OpDelete", got)
	}
}

func TestMapFSEventOp_Created(t *testing.T) {
	got := mapFSEventOp(fsevents.ItemCreated, "/tmp/file.go")
	if got != schema.OpCreate {
		t.Errorf("ItemCreated => %v, want OpCreate", got)
	}
}

func TestMapFSEventOp_Modified(t *testing.T) {
	got := mapFSEventOp(fsevents.ItemModified, "/tmp/file.go")
	if got != schema.OpModify {
		t.Errorf("ItemModified => %v, want OpModify", got)
	}
}

func TestMapFSEventOp_RenamedExists(t *testing.T) {
	// When a file is renamed but the path still exists, treat as modify.
	tmp := t.TempDir()
	f := filepath.Join(tmp, "exists.go")
	os.WriteFile(f, []byte("x"), 0644)

	got := mapFSEventOp(fsevents.ItemRenamed, f)
	if got != schema.OpModify {
		t.Errorf("ItemRenamed (file exists) => %v, want OpModify", got)
	}
}

func TestMapFSEventOp_RenamedGone(t *testing.T) {
	// When a file is renamed and the path no longer exists, treat as delete.
	got := mapFSEventOp(fsevents.ItemRenamed, "/nonexistent/path/gone.go")
	if got != schema.OpDelete {
		t.Errorf("ItemRenamed (file gone) => %v, want OpDelete", got)
	}
}

func TestMapFSEventOp_MetaOnly(t *testing.T) {
	// Metadata-only changes should return 0 (skip).
	metaFlags := []fsevents.EventFlags{
		fsevents.ItemInodeMetaMod,
		fsevents.ItemChangeOwner,
		fsevents.ItemXattrMod,
	}
	for _, flag := range metaFlags {
		got := mapFSEventOp(flag, "/tmp/file.go")
		if got != 0 {
			t.Errorf("flag %v => %v, want 0 (skip)", flag, got)
		}
	}
}

func TestMapFSEventOp_IsFileOnly(t *testing.T) {
	// ItemIsFile without other meaningful flags => OpModify fallback.
	got := mapFSEventOp(fsevents.ItemIsFile, "/tmp/file.go")
	if got != schema.OpModify {
		t.Errorf("ItemIsFile => %v, want OpModify", got)
	}
}

func TestMapFSEventOp_NoFlags(t *testing.T) {
	got := mapFSEventOp(0, "/tmp/file.go")
	if got != 0 {
		t.Errorf("no flags => %v, want 0", got)
	}
}

func TestMapFSEventOp_CombinedFlags_RemoveWins(t *testing.T) {
	// When multiple flags are set, the switch order determines priority.
	// ItemRemoved should take priority.
	flags := fsevents.ItemRemoved | fsevents.ItemModified
	got := mapFSEventOp(flags, "/tmp/file.go")
	if got != schema.OpDelete {
		t.Errorf("ItemRemoved|ItemModified => %v, want OpDelete", got)
	}
}

func TestMapFSEventOp_CombinedFlags_RenameBeforeCreate(t *testing.T) {
	// ItemRenamed takes priority over ItemCreated.
	flags := fsevents.ItemRenamed | fsevents.ItemCreated
	got := mapFSEventOp(flags, "/nonexistent/path/gone.go")
	if got != schema.OpDelete {
		t.Errorf("ItemRenamed|ItemCreated (gone) => %v, want OpDelete", got)
	}
}
