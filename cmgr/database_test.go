package cmgr

import (
	"fmt"
	"io/ioutil"
	"os"
	"path/filepath"
	"reflect"
	"strings"
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

	var version int
	if err = mgr.db.Get(&version, "PRAGMA user_version;"); err != nil {
		t.Fatalf("failed to inspect database version: %s", err)
	}
	if version != currentDatabaseVersion {
		t.Fatalf(
			"unexpected database version %d, expected %d",
			version,
			currentDatabaseVersion,
		)
	}
}

func TestDatabaseV0ToV1Migration(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "cmgr.db")
	db := newVersionZeroDatabase(t, dbPath)

	if _, err := db.Exec(`
		INSERT INTO challenges(
			id, name, namespace, challengetype, description, details,
			sourcechecksum, metadatachecksum, path, solvescript, templatable,
			maxusers, category, points
		) VALUES (
			'challenge', 'Challenge', '', 'custom', '', '', 0, 0,
			'/challenge', 0, 0, 0, '', 0
		);
		INSERT INTO hosts(challenge, name, idx, target)
			VALUES ('challenge', 'web', 0, 'web');
		INSERT INTO portNames(challenge, name, host, port)
			VALUES
				('challenge', 'http', 'web', 8080),
				('challenge', 'http', 'web', 8080);
	`); err != nil {
		t.Fatalf("failed to populate version 0 database: %s", err)
	}
	if err := db.Close(); err != nil {
		t.Fatalf("failed to close version 0 database: %s", err)
	}

	t.Setenv(DB_ENV, dbPath)
	mgr := &Manager{log: newLogger(DISABLED)}
	if err := mgr.initDatabase(); err != nil {
		t.Fatalf("failed to migrate database: %s", err)
	}
	defer mgr.db.Close()

	var version int
	if err := mgr.db.Get(&version, "PRAGMA user_version;"); err != nil {
		t.Fatalf("failed to inspect migrated database version: %s", err)
	}
	if version != currentDatabaseVersion {
		t.Fatalf(
			"unexpected migrated database version %d, expected %d",
			version,
			currentDatabaseVersion,
		)
	}

	for _, column := range []struct {
		table string
		name  string
	}{
		{table: "containerOptions", name: "seccomp"},
		{table: "builds", name: "requiredseccomptweaks"},
	} {
		var count int
		query := fmt.Sprintf(
			"SELECT COUNT(*) FROM pragma_table_info('%s') WHERE name = ?;",
			column.table,
		)
		if err := mgr.db.Get(&count, query, column.name); err != nil {
			t.Fatalf("failed to inspect migrated column: %s", err)
		}
		if count != 1 {
			t.Fatalf("missing migrated column %s.%s", column.table, column.name)
		}
	}

	var portCount int
	if err := mgr.db.Get(
		&portCount,
		`SELECT COUNT(*) FROM portNames
		 WHERE challenge = 'challenge' AND name = 'http';`,
	); err != nil {
		t.Fatalf("failed to inspect migrated published ports: %s", err)
	}
	if portCount != 1 {
		t.Fatalf("exact duplicates were not collapsed: got %d rows", portCount)
	}

	var endpointIndexCount int
	if err := mgr.db.Get(
		&endpointIndexCount,
		`SELECT COUNT(*) FROM sqlite_schema
		 WHERE type = 'index' AND name = 'portNamesEndpointIndex';`,
	); err != nil {
		t.Fatalf("failed to inspect migrated indexes: %s", err)
	}
	if endpointIndexCount != 1 {
		t.Fatal("version 1 indexes were not created")
	}
}

func TestDatabaseAdoptsUnversionedCurrentSchema(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "cmgr.db")
	db, err := sqlx.Open("sqlite", dbPath)
	if err != nil {
		t.Fatalf("failed to open unversioned database: %s", err)
	}
	if _, err := db.Exec(schemaQuery); err != nil {
		t.Fatalf("failed to create unversioned current schema: %s", err)
	}
	if err := db.Close(); err != nil {
		t.Fatalf("failed to close unversioned database: %s", err)
	}

	t.Setenv(DB_ENV, dbPath)
	mgr := &Manager{log: newLogger(DISABLED)}
	if err := mgr.initDatabase(); err != nil {
		t.Fatalf("failed to adopt unversioned current schema: %s", err)
	}
	defer mgr.db.Close()

	var version int
	if err := mgr.db.Get(&version, "PRAGMA user_version;"); err != nil {
		t.Fatalf("failed to inspect adopted database version: %s", err)
	}
	if version != currentDatabaseVersion {
		t.Fatalf(
			"unexpected adopted database version %d, expected %d",
			version,
			currentDatabaseVersion,
		)
	}
}

func TestDatabaseV0MigrationConflictRollsBack(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "cmgr.db")
	db := newVersionZeroDatabase(t, dbPath)

	if _, err := db.Exec(`
		INSERT INTO challenges(
			id, name, namespace, challengetype, description, details,
			sourcechecksum, metadatachecksum, path, solvescript, templatable,
			maxusers, category, points
		) VALUES (
			'challenge', 'Challenge', '', 'custom', '', '', 0, 0,
			'/challenge', 0, 0, 0, '', 0
		);
		INSERT INTO hosts(challenge, name, idx, target)
			VALUES ('challenge', 'web', 0, 'web');
		INSERT INTO portNames(challenge, name, host, port)
			VALUES
				('challenge', 'http', 'web', 8080),
				('challenge', 'http', 'web', 8080),
				('challenge', 'admin', 'web', 8080);
	`); err != nil {
		t.Fatalf("failed to populate conflicting version 0 database: %s", err)
	}
	if err := db.Close(); err != nil {
		t.Fatalf("failed to close version 0 database: %s", err)
	}

	t.Setenv(DB_ENV, dbPath)
	mgr := &Manager{log: newLogger(DISABLED)}
	err := mgr.initDatabase()
	if err == nil {
		if mgr.db != nil {
			_ = mgr.db.Close()
		}
		t.Fatal("ambiguous legacy endpoint aliases were migrated")
	}
	if !strings.Contains(err.Error(), "unique published endpoints") {
		t.Fatalf("unexpected migration error: %s", err)
	}
	if mgr.db != nil {
		t.Fatal("manager retained a database handle after failed migration")
	}

	db, err = sqlx.Open("sqlite", dbPath)
	if err != nil {
		t.Fatalf("failed to reopen rolled-back database: %s", err)
	}
	defer db.Close()

	var version int
	if err := db.Get(&version, "PRAGMA user_version;"); err != nil {
		t.Fatalf("failed to inspect rolled-back database version: %s", err)
	}
	if version != 0 {
		t.Fatalf("failed migration changed database version to %d", version)
	}

	var seccompColumnCount int
	if err := db.Get(
		&seccompColumnCount,
		"SELECT COUNT(*) FROM pragma_table_info('containerOptions') WHERE name = 'seccomp';",
	); err != nil {
		t.Fatalf("failed to inspect rolled-back columns: %s", err)
	}
	if seccompColumnCount != 0 {
		t.Fatal("failed migration left the seccomp column behind")
	}

	var portCount int
	if err := db.Get(&portCount, "SELECT COUNT(*) FROM portNames;"); err != nil {
		t.Fatalf("failed to inspect rolled-back published ports: %s", err)
	}
	if portCount != 3 {
		t.Fatalf("failed migration changed published ports: got %d rows", portCount)
	}

	var endpointIndexCount int
	if err := db.Get(
		&endpointIndexCount,
		`SELECT COUNT(*) FROM sqlite_schema
		 WHERE type = 'index' AND name = 'portNamesEndpointIndex';`,
	); err != nil {
		t.Fatalf("failed to inspect rolled-back indexes: %s", err)
	}
	if endpointIndexCount != 0 {
		t.Fatal("failed migration left version 1 indexes behind")
	}
}

func TestDatabaseRejectsFutureVersion(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "cmgr.db")
	db, err := sqlx.Open("sqlite", dbPath)
	if err != nil {
		t.Fatalf("failed to open temporary database: %s", err)
	}
	if _, err := db.Exec(`
		CREATE TABLE marker (value INTEGER);
		PRAGMA user_version = 2;
	`); err != nil {
		t.Fatalf("failed to create future database: %s", err)
	}
	if err := db.Close(); err != nil {
		t.Fatalf("failed to close future database: %s", err)
	}

	t.Setenv(DB_ENV, dbPath)
	mgr := &Manager{log: newLogger(DISABLED)}
	err = mgr.initDatabase()
	if err == nil {
		if mgr.db != nil {
			_ = mgr.db.Close()
		}
		t.Fatal("future database version was accepted")
	}
	if !strings.Contains(err.Error(), "newer than supported") {
		t.Fatalf("unexpected future-version error: %s", err)
	}
}

func newVersionZeroDatabase(t *testing.T, dbPath string) *sqlx.DB {
	t.Helper()

	db, err := sqlx.Open("sqlite", dbPath)
	if err != nil {
		t.Fatalf("failed to open version 0 database: %s", err)
	}
	if _, err := db.Exec(schemaQuery); err != nil {
		_ = db.Close()
		t.Fatalf("failed to create version 0 database fixture: %s", err)
	}
	if _, err := db.Exec(`
		DROP INDEX hostsOrderIndex;
		DROP INDEX portNamesNameIndex;
		DROP INDEX portNamesEndpointIndex;
		DROP INDEX imagesHostIndex;
		DROP INDEX imagePortsPortIndex;
		DROP INDEX lookupDataKeyIndex;
		DROP INDEX portAssignmentsNameIndex;
		DROP INDEX portAssignmentsPortIndex;
		DROP INDEX containerOptionsHostIndex;

		ALTER TABLE containerOptions DROP COLUMN seccomp;
		ALTER TABLE builds DROP COLUMN requiredseccomptweaks;
		PRAGMA user_version = 0;
	`); err != nil {
		_ = db.Close()
		t.Fatalf("failed to downgrade database fixture to version 0: %s", err)
	}
	return db
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
