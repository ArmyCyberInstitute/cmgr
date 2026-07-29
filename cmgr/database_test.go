package cmgr

import (
	"fmt"
	"io/ioutil"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

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

	var busyTimeoutMS int
	if err = mgr.db.Get(&busyTimeoutMS, "PRAGMA busy_timeout;"); err != nil {
		t.Fatalf("failed to inspect SQLite busy timeout: %s", err)
	}
	if busyTimeoutMS != sqliteBusyTimeoutMS {
		t.Fatalf(
			"unexpected SQLite busy timeout %dms, expected %dms",
			busyTimeoutMS,
			sqliteBusyTimeoutMS,
		)
	}
}

func TestDatabaseConcurrentWriterWaitsForLock(t *testing.T) {
	manager := newSchemaTestManager(t)
	txn, err := manager.db.Beginx()
	if err != nil {
		t.Fatal(err)
	}
	defer txn.Rollback()
	if _, err := txn.Exec(
		"INSERT INTO schemas(name, manual) VALUES ('lock-holder', 1);",
	); err != nil {
		t.Fatal(err)
	}

	result := make(chan error, 1)
	go func() {
		result <- manager.createSchemaRecord("waiting-writer", true)
	}()

	// Without a busy timeout SQLite rejects the second writer immediately.
	// Hold the first write transaction long enough for the other connection
	// to encounter the lock, then verify it resumes after commit.
	time.Sleep(100 * time.Millisecond)
	if err := txn.Commit(); err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-result:
		if err != nil {
			t.Fatalf("concurrent writer did not wait for the lock: %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("concurrent writer did not resume after the lock was released")
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
	if _, err := db.Exec(fmt.Sprintf(`
		CREATE TABLE marker (value INTEGER);
		PRAGMA user_version = %d;
	`, currentDatabaseVersion+1)); err != nil {
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
			CREATE TABLE retiredContainers (
				id TEXT NOT NULL PRIMARY KEY
			);
			CREATE TABLE retiredNetworks (
				name TEXT NOT NULL PRIMARY KEY
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

	var retired []string
	if err := db.Select(&retired, "SELECT id FROM retiredContainers ORDER BY id;"); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(retired, []string{"old-container"}) {
		t.Fatalf("swapped container was not retained for cleanup: %#v", retired)
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

	restored := &InstanceMetadata{
		Id:         7,
		Ports:      map[string]int{"http": 31007},
		Containers: []string{"old-container"},
	}
	if err = manager.replaceInstanceRuntimeMetadata(restored); err != nil {
		t.Fatalf("failed to restore runtime metadata: %s", err)
	}
	containers = nil
	if err := db.Select(
		&containers,
		"SELECT id FROM containers WHERE instance=7;",
	); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(containers, []string{"old-container"}) {
		t.Fatalf("old container was not restored atomically: %#v", containers)
	}
	retired = nil
	if err := db.Select(
		&retired,
		"SELECT id FROM retiredContainers ORDER BY id;",
	); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(retired, []string{"new-container"}) {
		t.Fatalf("replacement was not retired during rollback: %#v", retired)
	}
}

func TestIncompleteInstancesAreDiscoverableForStartupRecovery(t *testing.T) {
	manager := newSchemaTestManager(t)
	insertConstraintChallenge(t, manager.db)
	insertConstraintBuild(t, manager.db)
	insertConstraintInstance(t, manager.db)

	instances, err := manager.incompleteInstanceIDs()
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(instances, []InstanceId{1}) {
		t.Fatalf("unexpected incomplete instances: %#v", instances)
	}
	requireExec(
		t,
		manager.db,
		"INSERT INTO containers(instance, id) VALUES (1, 'active');",
	)
	instances, err = manager.incompleteInstanceIDs()
	if err != nil {
		t.Fatal(err)
	}
	if len(instances) != 0 {
		t.Fatalf("active instance was marked incomplete: %#v", instances)
	}
}

func TestUpdateChallengesPreservesFailureAndRollsBack(t *testing.T) {
	manager := newSchemaTestManager(t)
	original := newAddChallengeTestMetadata(
		"transaction-test",
		map[string]PortInfo{"http": {Host: "web", Port: 8080}},
	)
	original.Tags = []string{"stable"}
	if err := manager.addChallenge(original); err != nil {
		t.Fatal(err)
	}
	if _, err := manager.db.Exec(`
		CREATE TRIGGER reject_bad_tag
		BEFORE INSERT ON tags
		WHEN NEW.tag = 'reject-me'
		BEGIN
			SELECT RAISE(ABORT, 'forced tag failure');
		END;
	`); err != nil {
		t.Fatal(err)
	}

	updated := *original
	updated.Name = "Changed"
	updated.Tags = []string{"reject-me"}
	errs := manager.updateChallengesInternal(
		[]*ChallengeMetadata{&updated},
		false,
		false,
	)
	if len(errs) != 1 || !strings.Contains(errs[0].Error(), "forced tag failure") {
		t.Fatalf("original update failure was not returned: %v", errs)
	}
	persisted, err := manager.lookupChallengeMetadata(original.Id)
	if err != nil {
		t.Fatal(err)
	}
	if persisted.Name != original.Name ||
		!reflect.DeepEqual(persisted.Tags, original.Tags) {
		t.Fatalf("failed update was partially committed: %#v", persisted)
	}
}

func TestNetworkOptionsRoundTrip(t *testing.T) {
	manager := newSchemaTestManager(t)
	metadata := newAddChallengeTestMetadata("egress", nil)
	metadata.ChallengeOptions.AllowEgress = true
	if err := manager.addChallenge(metadata); err != nil {
		t.Fatal(err)
	}
	loaded, err := manager.lookupChallengeMetadata(metadata.Id)
	if err != nil {
		t.Fatal(err)
	}
	if !loaded.ChallengeOptions.AllowEgress {
		t.Fatal("allow_egress was not persisted")
	}
}

func TestEmptyManagedSchemaIsPersistedAndListed(t *testing.T) {
	manager := newSchemaTestManager(t)
	if err := manager.createSchemaRecord("empty", false); err != nil {
		t.Fatal(err)
	}
	schemas, err := manager.queryForSchemas()
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(schemas, []string{"empty"}) {
		t.Fatalf("empty schema was lost: %#v", schemas)
	}
}

func TestCurrentDatabaseSchemaIsValidatedAtStartup(t *testing.T) {
	manager := newSchemaTestManager(t)
	if _, err := manager.db.Exec("DROP INDEX schemaIndex;"); err != nil {
		t.Fatal(err)
	}
	err := ensureDatabaseSchema(manager.db)
	if err == nil || !strings.Contains(err.Error(), "schemaIndex") {
		t.Fatalf("missing current-version index was not detected: %v", err)
	}
}

func TestCurrentDatabaseOwnershipInvariantsAreValidatedAtStartup(t *testing.T) {
	tests := []struct {
		name  string
		setup func(*testing.T, *Manager)
		match string
	}{
		{
			name: "orphaned build schema",
			setup: func(t *testing.T, manager *Manager) {
				insertConstraintChallenge(t, manager.db)
				requireExec(
					t,
					manager.db,
					"INSERT INTO networkOptions(challenge, allowegress) VALUES ('challenge', 0);",
				)
				insertConstraintBuild(t, manager.db)
			},
			match: "every build belongs to a defined schema",
		},
		{
			name: "missing network options",
			setup: func(t *testing.T, manager *Manager) {
				insertConstraintChallenge(t, manager.db)
			},
			match: "every challenge has network options",
		},
		{
			name: "active container pending cleanup",
			setup: func(t *testing.T, manager *Manager) {
				insertConstraintChallenge(t, manager.db)
				requireExec(
					t,
					manager.db,
					"INSERT INTO networkOptions(challenge, allowegress) VALUES ('challenge', 0);",
				)
				requireExec(
					t,
					manager.db,
					"INSERT INTO schemas(name, manual) VALUES ('schema', 0);",
				)
				insertConstraintBuild(t, manager.db)
				insertConstraintInstance(t, manager.db)
				requireExec(
					t,
					manager.db,
					"INSERT INTO containers(instance, id) VALUES (1, 'overlap');",
				)
				requireExec(
					t,
					manager.db,
					"INSERT INTO retiredContainers(id) VALUES ('overlap');",
				)
			},
			match: "active containers are not pending cleanup",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			manager := newSchemaTestManager(t)
			test.setup(t, manager)
			err := ensureDatabaseSchema(manager.db)
			if err == nil || !strings.Contains(err.Error(), test.match) {
				t.Fatalf("database invariant violation was not detected: %v", err)
			}
		})
	}
}

func TestDatabaseV2MigrationInfersSchemaOwnership(t *testing.T) {
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
		INSERT INTO builds(
			flag, format, seed, hasartifacts, lastsolved, challenge, schema,
			instancecount
		) VALUES (
			'flag', 'flag{%s}', 1, 0, 0, 'challenge', 'manual-legacy', -1
		);
	`); err != nil {
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	t.Setenv(DB_ENV, dbPath)
	manager := &Manager{log: newLogger(DISABLED)}
	if err := manager.initDatabase(); err != nil {
		t.Fatal(err)
	}
	defer manager.db.Close()

	manual, err := manager.schemaIsManual("manual-legacy")
	if err != nil {
		t.Fatal(err)
	}
	if !manual {
		t.Fatal("legacy manual schema was migrated as managed")
	}
	var allowEgress bool
	if err := manager.db.Get(
		&allowEgress,
		"SELECT allowegress FROM networkOptions WHERE challenge='challenge';",
	); err != nil {
		t.Fatal(err)
	}
	if allowEgress {
		t.Fatal("migration enabled egress for a legacy challenge")
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
