package selfupdate

import "testing"

func TestParseVersion(t *testing.T) {
	for _, tc := range []struct {
		in string
		ok bool
		v  Version
	}{
		{"v1.9.0", true, Version{1, 9, 0, ""}},
		{"1.10.2", true, Version{1, 10, 2, ""}},
		{"v2.0.0-rc.1", true, Version{2, 0, 0, "rc.1"}},
		{"v1.2.3+build.5", true, Version{1, 2, 3, ""}},
		{"dev", false, Version{}},
		{"", false, Version{}},
		{"v1.2", false, Version{}},
		{"v1.2.x", false, Version{}},
		{"v1.2.3-", false, Version{}},
	} {
		v, ok := ParseVersion(tc.in)
		if ok != tc.ok || v != tc.v {
			t.Errorf("ParseVersion(%q) = %+v, %v; want %+v, %v", tc.in, v, ok, tc.v, tc.ok)
		}
	}
}

func TestIsNewer(t *testing.T) {
	for _, tc := range []struct {
		candidate, current string
		want               bool
	}{
		{"v1.10.0", "v1.9.0", true},
		{"v1.9.0", "v1.10.0", false},
		{"v1.9.0", "v1.9.0", false},
		{"v1.9.1", "1.9.0", true},
		{"v2.0.0", "v2.0.0-rc.2", true},
		{"v2.0.0-rc.2", "v2.0.0", false},
		{"v2.0.0-rc.10", "v2.0.0-rc.2", true},
		{"v2.0.0-rc.1", "v2.0.0-beta.9", true},
		{"v9.9.9", "dev", false},
		{"", "v1.0.0", false},
	} {
		if got := IsNewer(tc.candidate, tc.current); got != tc.want {
			t.Errorf("IsNewer(%q, %q) = %v, want %v", tc.candidate, tc.current, got, tc.want)
		}
	}
}

func TestAssetName(t *testing.T) {
	if got := AssetName("v1.9.0", "linux", "amd64"); got != "belay_1.9.0_linux_amd64.tar.gz" {
		t.Errorf("linux asset = %q", got)
	}
	if got := AssetName("1.9.0", "windows", "amd64"); got != "belay_1.9.0_windows_amd64.zip" {
		t.Errorf("windows asset = %q", got)
	}
}

func TestParseChecksums(t *testing.T) {
	sum := "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"
	data := []byte(sum + "  belay_1.0.0_linux_amd64.tar.gz\nnot a line\n" + "zz  other\n")
	got := ParseChecksums(data)
	if len(got) != 1 || got["belay_1.0.0_linux_amd64.tar.gz"] != sum {
		t.Fatalf("ParseChecksums = %v", got)
	}
}
