package cmgr

import (
	"testing"

	"io/ioutil"
	"os"
)

func TestInitDatabase(t *testing.T) {
	dbFile, err := ioutil.TempFile("", "*.db")
	if err != nil {
		t.Fatalf("failed to make temporary file: %s", err)
	}
	defer os.Remove(dbFile.Name()) // Clean up after ourselves

	dbFile.Close() // Do not need it open

	// Minimal stub of the manager
	mgr := new(Manager)
	mgr.log = newLogger(DISABLED)
	os.Setenv(DB_ENV, dbFile.Name())

	err = mgr.initDatabase()
	if err != nil {
		t.Fatalf("failed to initialize database: %s", err)
	}

	//TODO(jrolli): Do the actual constraint checking...
}
