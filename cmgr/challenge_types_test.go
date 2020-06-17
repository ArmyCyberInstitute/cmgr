package cmgr

import (
	"testing"

	"io/ioutil"
	"os"
	"path/filepath"
	"strings"
)

func TestDockerfileConsistency(t *testing.T) {
	mgr := new(Manager)
	mgr.log = newLogger(DISABLED)
	err := mgr.initDocker()

	if err != nil {
		t.Fatalf("could not create a manager")
	}

	err = filepath.Walk("dockerfiles", func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}

		if info.IsDir() {
			if info.Name() == "dockerfiles" {
				return nil
			}
			t.Errorf("unknown directory inside dockerfiles")
			return filepath.SkipDir
		}

		if filepath.Ext(path) != ".Dockerfile" {
			t.Errorf("unknown file extension of '%s' for %s", filepath.Ext(path), info.Name())
			return nil
		}

		cType := info.Name()[:len(info.Name())-len(".Dockerfile")]

		f, err := os.Open(path)
		if err != nil {
			t.Errorf("could not open %s", info.Name())
			return nil
		}

		diskDfBytes, err := ioutil.ReadAll(f)
		f.Close()
		if err != nil {
			t.Errorf("could not read %s", info.Name())
			return nil
		}

		mgrDfBytes := mgr.getDockerfile(cType)
		if mgrDfBytes == nil {
			t.Errorf("no challenge type of '%s' present in cmgr", cType)
			return nil
		}

		diskDf := strings.TrimSpace(string(diskDfBytes))
		mgrDf := strings.TrimSpace(string(mgrDfBytes))

		if mgrDf != diskDf {
			t.Errorf("cmgr and disk have different versions of Dockerfile for '%s'", cType)
		}

		return nil
	})

	if err != nil {
		t.Fatalf("error occurred during walk: %s", err)
	}
}
