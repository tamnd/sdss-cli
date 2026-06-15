package sdss

import (
	"strings"
	"testing"
)

// These tests are offline: they exercise the URI driver's pure string functions.
// The client's HTTP behaviour is covered in sdss_test.go.

func TestDomainInfo(t *testing.T) {
	info := Domain{}.Info()
	if info.Scheme != "sdss" {
		t.Errorf("Scheme = %q, want sdss", info.Scheme)
	}
	if len(info.Hosts) == 0 || info.Hosts[0] != Host {
		t.Errorf("Hosts = %v, want [%s]", info.Hosts, Host)
	}
	if info.Identity.Binary != "sdss" {
		t.Errorf("Identity.Binary = %q, want sdss", info.Identity.Binary)
	}
}

func TestClassify(t *testing.T) {
	cases := []struct {
		in  string
		typ string
		id  string
	}{
		{"1237645876861272067", "object", "1237645876861272067"},
		{"299489248914612224", "object", "299489248914612224"},
		{"some-ref", "object", "some-ref"},
	}
	for _, tc := range cases {
		typ, id, err := Domain{}.Classify(tc.in)
		if err != nil {
			t.Errorf("Classify(%q) returned unexpected error: %v", tc.in, err)
			continue
		}
		if typ != tc.typ {
			t.Errorf("Classify(%q) type = %q, want %q", tc.in, typ, tc.typ)
		}
		if id != tc.id {
			t.Errorf("Classify(%q) id = %q, want %q", tc.in, id, tc.id)
		}
	}
}

func TestClassifyEmpty(t *testing.T) {
	_, _, err := Domain{}.Classify("")
	if err == nil {
		t.Fatal("expected error for empty input")
	}
}

func TestLocate(t *testing.T) {
	got, err := Domain{}.Locate("object", "1237645876861272067")
	if err != nil {
		t.Fatalf("Locate error: %v", err)
	}
	if !strings.Contains(got, Host) {
		t.Errorf("Locate = %q, expected to contain %q", got, Host)
	}
	if !strings.Contains(got, "1237645876861272067") {
		t.Errorf("Locate = %q, expected to contain object ID", got)
	}
	want := "https://" + Host + "/dr18/SkyServerWS/SearchTools/GetObjDetailStr?objId=1237645876861272067"
	if got != want {
		t.Errorf("Locate = %q, want %q", got, want)
	}
}

func TestLocateUnknownType(t *testing.T) {
	_, err := Domain{}.Locate("unknown", "123")
	if err == nil {
		t.Fatal("expected error for unknown resource type")
	}
}

func TestPhotoType(t *testing.T) {
	cases := []struct {
		in   int
		want string
	}{
		{3, "galaxy"},
		{6, "star"},
		{0, "0"},
		{99, "99"},
	}
	for _, tc := range cases {
		got := photoType(tc.in)
		if got != tc.want {
			t.Errorf("photoType(%d) = %q, want %q", tc.in, got, tc.want)
		}
	}
}
