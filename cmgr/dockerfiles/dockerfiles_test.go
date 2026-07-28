package dockerfiles

import (
	"bytes"
	"strings"
	"testing"
)

func TestGetDefaultsToUbuntu24(t *testing.T) {
	dockerfile, err := Get("flask")
	if err != nil {
		t.Fatalf("could not load flask Dockerfile: %s", err)
	}
	if !bytes.Contains(dockerfile, []byte("FROM "+Ubuntu24Image)) {
		t.Fatal("flask does not use the supported Ubuntu 24.04 baseline")
	}
}

func TestGetOffersExplicitUbuntu26Variant(t *testing.T) {
	dockerfile, err := Get("flask-ubuntu26")
	if err != nil {
		t.Fatalf("could not load Ubuntu 26.04 variant: %s", err)
	}
	if !bytes.Contains(dockerfile, []byte("FROM "+Ubuntu26Image)) {
		t.Fatal("explicit variant does not use Ubuntu 26.04")
	}
	if bytes.Contains(dockerfile, []byte("FROM "+Ubuntu24Image)) {
		t.Fatal("explicit variant retained the Ubuntu 24.04 base")
	}
}

func TestGetRejectsUbuntu26ForNonUbuntuDriver(t *testing.T) {
	if _, err := Get("node-ubuntu26"); err == nil {
		t.Fatal("Node driver unexpectedly accepted an Ubuntu variant")
	}
}

func TestHacksportSupportFilesIncludeAttributionAndRunner(t *testing.T) {
	files, err := SupportFiles("hacksport")
	if err != nil {
		t.Fatalf("could not load hacksport support files: %s", err)
	}
	foundLicense := false
	foundRunner := false
	for _, file := range files {
		switch file.Name {
		case ".cmgr/hacksport_compat/LICENSE.picoCTF":
			foundLicense = strings.Contains(
				string(file.Data),
				"Carnegie Mellon University",
			)
		case ".cmgr/hacksport_compat/cmgr_hacksport/runner.py":
			foundRunner = strings.Contains(
				string(file.Data),
				"fb09fa2cb745c2db007dc4be8f95e37a1788c830",
			)
		}
	}
	if !foundLicense {
		t.Fatal("embedded hacksport support files omit the upstream license")
	}
	if !foundRunner {
		t.Fatal("embedded hacksport runner omits its upstream source reference")
	}

	legacyFiles, err := SupportFiles("hacksport-legacy")
	if err != nil {
		t.Fatalf("could not inspect legacy support files: %s", err)
	}
	if len(legacyFiles) != 0 {
		t.Fatal("legacy hacksport driver unexpectedly embeds the modern runner")
	}
}
