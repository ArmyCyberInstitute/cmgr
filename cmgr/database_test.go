package cmgr

import (
	"io/ioutil"
	"os"
	"reflect"
	"testing"

	"github.com/jmoiron/sqlx"
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
	defer mgr.db.Close()
	defer os.Unsetenv(DB_ENV)

	var seccompColumns int
	err = mgr.db.Get(&seccompColumns, "SELECT COUNT(*) FROM pragma_table_info('containerOptions') WHERE name = 'seccomp';")
	if err != nil {
		t.Fatalf("failed to inspect database schema: %s", err)
	}
	if seccompColumns != 1 {
		t.Fatalf("database does not contain the seccomp options column")
	}

}

func TestSeccompDatabaseMigration(t *testing.T) {
	dbFile, err := ioutil.TempFile("", "*.db")
	if err != nil {
		t.Fatalf("failed to make temporary file: %s", err)
	}
	dbPath := dbFile.Name()
	dbFile.Close()
	defer os.Remove(dbPath)

	db, err := sqlx.Open("sqlite", dbPath)
	if err != nil {
		t.Fatalf("failed to open temporary database: %s", err)
	}
	_, err = db.Exec(`
		CREATE TABLE containerOptions (
			challenge INTEGER NOT NULL,
			host TEXT NOT NULL
		);
	`)
	db.Close()
	if err != nil {
		t.Fatalf("failed to create legacy database schema: %s", err)
	}

	mgr := new(Manager)
	mgr.log = newLogger(DISABLED)
	os.Setenv(DB_ENV, dbPath)
	defer os.Unsetenv(DB_ENV)

	if err = mgr.initDatabase(); err != nil {
		t.Fatalf("failed to migrate database: %s", err)
	}
	defer mgr.db.Close()

	var seccompColumns int
	err = mgr.db.Get(&seccompColumns, "SELECT COUNT(*) FROM pragma_table_info('containerOptions') WHERE name = 'seccomp';")
	if err != nil {
		t.Fatalf("failed to inspect migrated database schema: %s", err)
	}
	if seccompColumns != 1 {
		t.Fatalf("legacy database was not migrated")
	}
}

func TestReplaceInstanceRuntimeMetadataPreservesAssignedPorts(t *testing.T) {
	db, err := sqlx.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatalf("failed to open in-memory database: %s", err)
	}
	defer db.Close()
	if _, err = db.Exec(`
		CREATE TABLE portAssignments (
			instance INTEGER NOT NULL,
			name TEXT NOT NULL,
			port INTEGER NOT NULL
		);
		CREATE TABLE containers (
			instance INTEGER NOT NULL,
			id TEXT NOT NULL PRIMARY KEY
		);
		INSERT INTO portAssignments(instance, name, port)
			VALUES (7, 'http', 31007), (8, 'http', 31008);
		INSERT INTO containers(instance, id)
			VALUES (7, 'old-container'), (8, 'other-container');
	`); err != nil {
		t.Fatalf("failed to create runtime metadata fixture: %s", err)
	}

	manager := &Manager{db: db}
	candidate := &InstanceMetadata{
		Id:         7,
		Ports:      map[string]int{"http": 31007},
		Containers: []string{"new-container"},
	}
	if err = manager.replaceInstanceRuntimeMetadata(candidate); err != nil {
		t.Fatalf("failed to replace runtime metadata: %s", err)
	}

	var ports []struct {
		Instance int
		Name     string
		Port     int
	}
	if err = db.Select(
		&ports,
		"SELECT instance, name, port FROM portAssignments ORDER BY instance;",
	); err != nil {
		t.Fatalf("failed to inspect port assignments: %s", err)
	}
	expectedPorts := []struct {
		Instance int
		Name     string
		Port     int
	}{
		{Instance: 7, Name: "http", Port: 31007},
		{Instance: 8, Name: "http", Port: 31008},
	}
	if !reflect.DeepEqual(ports, expectedPorts) {
		t.Fatalf("unexpected port assignments: %#v", ports)
	}

	var containers []string
	if err = db.Select(
		&containers,
		"SELECT id FROM containers WHERE instance=7;",
	); err != nil {
		t.Fatalf("failed to inspect container assignments: %s", err)
	}
	if !reflect.DeepEqual(containers, []string{"new-container"}) {
		t.Fatalf("unexpected replacement containers: %#v", containers)
	}
}
