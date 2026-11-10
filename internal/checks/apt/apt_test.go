package apt

import (
	"reflect"
	"testing"
)

func TestParseAutoremove(t *testing.T) {
	out := []byte(`NOTE: This is only a simulation!
Reading package lists...
The following packages will be REMOVED:
  dkms libfoo1
Remv dkms [3.0.11-1ubuntu13]
Remv libfoo1:i386 [1.2-3]
`)
	if got, want := parseAutoremove(out), []string{"dkms", "libfoo1:i386"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("got %v, want %v", got, want)
	}
}

func TestParseStatus(t *testing.T) {
	out := []byte("ii \tdkms\t210\nrc \tlibnvidia-compute-535:amd64\t\nii \tlibc6:amd64\t13000\n")
	st, sz := parseStatus(out)
	if st["libnvidia-compute-535:amd64"] != "rc" || st["dkms"] != "ii" {
		t.Fatalf("status %v", st)
	}
	if sizeOf(sz, "dkms") != 210*1024 || sizeOf(sz, "libc6") != 13000*1024 || sizeOf(sz, "nope") != 0 {
		t.Fatalf("sizes %v", sz)
	}
}
