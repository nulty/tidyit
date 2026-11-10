package flatpak

import (
	"reflect"
	"testing"
)

func TestUnused(t *testing.T) {
	apps := parseApps([]byte("org.gimp.GIMP/x86_64/stable\torg.gnome.Platform/x86_64/46\n" +
		"com.example.Old/x86_64/stable\torg.freedesktop.Platform/x86_64/23.08\n"))
	if len(apps) != 2 {
		t.Fatalf("apps: %v", apps)
	}
	sdk, ok := metadataSDK([]byte("[Application]\nname=org.gimp.GIMP\nruntime=org.gnome.Platform/x86_64/46\nsdk=org.gnome.Sdk/x86_64/46\n\n[Context]\nsdk=nope\n"))
	if !ok || sdk.String() != "org.gnome.Sdk/x86_64/46" {
		t.Fatalf("sdk: %v %v", sdk, ok)
	}
	apps[0].sdk, apps[0].sdkKnown = sdk, true
	apps[1].sdkKnown = true // no sdk in its metadata

	runtimes := parseRefs([]byte(`org.gnome.Platform/x86_64/46
org.gnome.Platform/x86_64/44
org.gnome.Platform.Locale/x86_64/46
org.gnome.Sdk/x86_64/46
org.gnome.Sdk/x86_64/44
org.freedesktop.Platform/x86_64/23.08
org.freedesktop.Platform.GL.default/x86_64/24.08
org.kde.Platform/x86_64/6.7
org.kde.Platform.Locale/x86_64/6.7
`))
	var got []string
	for _, r := range unused(runtimes, apps) {
		got = append(got, r.String())
	}
	want := []string{"org.gnome.Platform/x86_64/44", "org.gnome.Sdk/x86_64/44", "org.kde.Platform/x86_64/6.7", "org.kde.Platform.Locale/x86_64/6.7"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %v\nwant %v", got, want)
	}

	// With an unknown sdk, SDKs are never flagged.
	apps[1].sdkKnown = false
	for _, r := range unused(runtimes, apps) {
		if r.id == "org.gnome.Sdk" {
			t.Fatalf("flagged %v although an sdk is unknown", r)
		}
	}
}
