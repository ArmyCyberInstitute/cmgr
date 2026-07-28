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

	var requiredSeccompTweaksColumns int
	err = mgr.db.Get(
		&requiredSeccompTweaksColumns,
		"SELECT COUNT(*) FROM pragma_table_info('builds') WHERE name = 'requiredseccomptweaks';",
	)
	if err != nil {
		t.Fatalf("failed to inspect database schema: %s", err)
	}
	if requiredSeccompTweaksColumns != 1 {
		t.Fatalf("database does not contain the build seccomp requirements column")
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

func TestBuildSeccompRequirementsDatabaseMigration(t *testing.T) {
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
		CREATE TABLE builds (
			id INTEGER PRIMARY KEY,
			flag TEXT NOT NULL,
			format TEXT NOT NULL,
			seed INTEGER NOT NULL,
			hasartifacts INTEGER NOT NULL,
			lastsolved INTEGER,
			challenge TEXT NOT NULL,
			schema TEXT NOT NULL,
			instancecount INT NOT NULL
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

	var columns int
	err = mgr.db.Get(
		&columns,
		"SELECT COUNT(*) FROM pragma_table_info('builds') WHERE name = 'requiredseccomptweaks';",
	)
	if err != nil {
		t.Fatalf("failed to inspect migrated database schema: %s", err)
	}
	if columns != 1 {
		t.Fatalf("legacy database was not migrated")
	}

	var defaultValue string
	if err = mgr.db.Get(
		&defaultValue,
		"SELECT dflt_value FROM pragma_table_info('builds') WHERE name = 'requiredseccomptweaks';",
	); err != nil {
		t.Fatalf("failed to inspect migrated column default: %s", err)
	}
	if defaultValue != "'[]'" {
		t.Fatalf("unexpected migrated column default %q", defaultValue)
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

func TestOpenCompletedBuildRestoresRuntimeMetadata(t *testing.T) {
	manager := newSchemaTestManager(t)
	insertConstraintChallenge(t, manager.db)

	build := &BuildMetadata{
		Seed:          58,
		Format:        "flag{%s}",
		Challenge:     "challenge",
		Schema:        "schema",
		InstanceCount: 1,
	}
	if err := manager.openBuild(build); err != nil {
		t.Fatalf("failed to open new build: %s", err)
	}
	build.Flag = "flag{persisted}"
	build.RequiredSeccompTweaks = SeccompTweakList{
		seccompTweakAllowDisableASLR,
	}
	build.LookupData = map[string]string{"lookup": "value"}
	build.Images = []Image{
		{Host: "challenge", Ports: []string{"5000/tcp"}},
	}
	if err := manager.finalizeBuild(build); err != nil {
		t.Fatalf("failed to finalize build: %s", err)
	}

	reopened := &BuildMetadata{
		Seed:          build.Seed,
		Format:        build.Format,
		Challenge:     build.Challenge,
		Schema:        build.Schema,
		InstanceCount: 2,
	}
	if err := manager.openBuild(reopened); err != nil {
		t.Fatalf("failed to reopen completed build: %s", err)
	}
	if !reflect.DeepEqual(
		reopened.RequiredSeccompTweaks,
		build.RequiredSeccompTweaks,
	) {
		t.Fatalf(
			"required seccomp tweaks were not restored: %#v",
			reopened.RequiredSeccompTweaks,
		)
	}
	if !reflect.DeepEqual(reopened.LookupData, build.LookupData) {
		t.Fatalf("lookup data was not restored: %#v", reopened.LookupData)
	}
	if len(reopened.Images) != 1 ||
		reopened.Images[0].Host != build.Images[0].Host ||
		!reflect.DeepEqual(reopened.Images[0].Ports, build.Images[0].Ports) {
		t.Fatalf("images were not restored: %#v", reopened.Images)
	}
	if reopened.InstanceCount != 2 {
		t.Fatalf(
			"reopened build did not retain updated instance count: %d",
			reopened.InstanceCount,
		)
	}
}
