package cmgr

import (
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"testing"

	"github.com/jmoiron/sqlx"
)

func TestInitDatabaseCreatesExpectedSchema(t *testing.T) {
	manager := newSchemaTestManager(t)

	var foreignKeysEnabled bool
	if err := manager.db.Get(&foreignKeysEnabled, "PRAGMA foreign_keys;"); err != nil {
		t.Fatalf("could not inspect foreign-key enforcement: %s", err)
	}
	if !foreignKeysEnabled {
		t.Fatal("foreign-key enforcement is disabled")
	}

	var tables []string
	if err := manager.db.Select(
		&tables,
		"SELECT name FROM sqlite_schema WHERE type='table' AND name NOT LIKE 'sqlite_%' ORDER BY name;",
	); err != nil {
		t.Fatalf("could not inspect database tables: %s", err)
	}
	expectedTables := []string{
		"attributes",
		"builds",
		"challenges",
		"containerOptions",
		"containers",
		"hints",
		"imagePorts",
		"images",
		"instances",
		"lookupData",
		"networkOptions",
		"portAssignments",
		"portNames",
		"retiredContainers",
		"retiredNetworks",
		"schemas",
		"hosts",
		"tags",
	}
	sort.Strings(expectedTables)
	if !reflect.DeepEqual(tables, expectedTables) {
		t.Fatalf("unexpected database tables:\ngot:  %#v\nwant: %#v", tables, expectedTables)
	}

	var indexes []string
	if err := manager.db.Select(
		&indexes,
		"SELECT name FROM sqlite_schema WHERE type='index' AND name NOT LIKE 'sqlite_%' ORDER BY name;",
	); err != nil {
		t.Fatalf("could not inspect database indexes: %s", err)
	}
	expectedIndexes := []string{
		"attributeIndex",
		"containerOptionsHostIndex",
		"hostsIndex",
		"hostsOrderIndex",
		"imagePortsPortIndex",
		"imagesHostIndex",
		"lookupDataKeyIndex",
		"portAssignmentsNameIndex",
		"portAssignmentsPortIndex",
		"portNamesEndpointIndex",
		"portNamesNameIndex",
		"schemaIndex",
		"tagIndex",
	}
	sort.Strings(expectedIndexes)
	if !reflect.DeepEqual(indexes, expectedIndexes) {
		t.Fatalf("unexpected database indexes:\ngot:  %#v\nwant: %#v", indexes, expectedIndexes)
	}

	insertCompleteConstraintFixture(t, manager.db)

	var foreignKeyProblems int
	if err := manager.db.Get(
		&foreignKeyProblems,
		"SELECT COUNT(*) FROM pragma_foreign_key_check;",
	); err != nil {
		t.Fatalf("could not run foreign-key check: %s", err)
	}
	if foreignKeyProblems != 0 {
		t.Fatalf("valid fixture produced %d foreign-key errors", foreignKeyProblems)
	}

	var seccomp string
	if err := manager.db.Get(
		&seccomp,
		"SELECT seccomp FROM containerOptions WHERE challenge='challenge' AND host='web';",
	); err != nil {
		t.Fatalf("could not inspect default seccomp value: %s", err)
	}
	if seccomp != "" {
		t.Fatalf("unexpected default seccomp value %q", seccomp)
	}
}

func TestDatabaseCheckAndNotNullConstraints(t *testing.T) {
	tests := []struct {
		name  string
		setup func(*testing.T, *sqlx.DB)
		query string
		args  []interface{}
	}{
		{
			name:  "challenge name is required",
			query: challengeConstraintInsert,
			args: []interface{}{
				"invalid", nil, 0, 0, 0, 0,
			},
		},
		{
			name:  "solve script is boolean",
			query: challengeConstraintInsert,
			args: []interface{}{
				"invalid", "Invalid", 2, 0, 0, 0,
			},
		},
		{
			name:  "templatable is boolean",
			query: challengeConstraintInsert,
			args: []interface{}{
				"invalid", "Invalid", 0, -1, 0, 0,
			},
		},
		{
			name:  "max users is nonnegative",
			query: challengeConstraintInsert,
			args: []interface{}{
				"invalid", "Invalid", 0, 0, -1, 0,
			},
		},
		{
			name:  "points are nonnegative",
			query: challengeConstraintInsert,
			args: []interface{}{
				"invalid", "Invalid", 0, 0, 0, -1,
			},
		},
		{
			name:  "build artifact marker is boolean",
			setup: insertConstraintChallenge,
			query: `INSERT INTO builds(
				id, flag, format, seed, hasartifacts, lastsolved,
				challenge, schema, instancecount
			) VALUES (1, 'flag', 'flag{%s}', 1, 2, 0, 'challenge', 'schema', 1);`,
		},
		{
			name: "published port rejects zero",
			setup: func(t *testing.T, db *sqlx.DB) {
				insertConstraintChallenge(t, db)
				insertConstraintHost(t, db)
			},
			query: `INSERT INTO portNames(challenge, name, host, port)
				VALUES ('challenge', 'http', 'web', 0);`,
		},
		{
			name: "published port rejects 65536",
			setup: func(t *testing.T, db *sqlx.DB) {
				insertConstraintChallenge(t, db)
				insertConstraintHost(t, db)
			},
			query: `INSERT INTO portNames(challenge, name, host, port)
				VALUES ('challenge', 'http', 'web', 65536);`,
		},
		{
			name: "assigned port rejects zero",
			setup: func(t *testing.T, db *sqlx.DB) {
				insertConstraintChallenge(t, db)
				insertConstraintBuild(t, db)
				insertConstraintInstance(t, db)
			},
			query: `INSERT INTO portAssignments(instance, name, port)
				VALUES (1, 'http', 0);`,
		},
		{
			name: "assigned port rejects 65536",
			setup: func(t *testing.T, db *sqlx.DB) {
				insertConstraintChallenge(t, db)
				insertConstraintBuild(t, db)
				insertConstraintInstance(t, db)
			},
			query: `INSERT INTO portAssignments(instance, name, port)
				VALUES (1, 'http', 65536);`,
		},
		{
			name:  "container init is boolean",
			setup: insertConstraintChallenge,
			query: containerOptionsConstraintInsert,
			args:  []interface{}{2, 0, 0},
		},
		{
			name:  "read-only root filesystem is boolean",
			setup: insertConstraintChallenge,
			query: containerOptionsConstraintInsert,
			args:  []interface{}{0, -1, 0},
		},
		{
			name:  "no-new-privileges is boolean",
			setup: insertConstraintChallenge,
			query: containerOptionsConstraintInsert,
			args:  []interface{}{0, 0, 2},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			manager := newSchemaTestManager(t)
			if test.setup != nil {
				test.setup(t, manager.db)
			}
			requireConstraintFailure(t, manager.db, test.query, test.args...)
		})
	}
}

func TestDatabaseForeignKeyConstraints(t *testing.T) {
	manager := newSchemaTestManager(t)
	tests := []struct {
		name  string
		query string
	}{
		{
			name:  "hint challenge",
			query: `INSERT INTO hints(challenge, idx, hint) VALUES ('missing', 0, 'hint');`,
		},
		{
			name:  "tag challenge",
			query: `INSERT INTO tags(challenge, tag) VALUES ('missing', 'tag');`,
		},
		{
			name:  "attribute challenge",
			query: `INSERT INTO attributes(challenge, key, value) VALUES ('missing', 'key', 'value');`,
		},
		{
			name:  "host challenge",
			query: `INSERT INTO hosts(challenge, name, idx, target) VALUES ('missing', 'web', 0, 'web');`,
		},
		{
			name:  "port challenge and host",
			query: `INSERT INTO portNames(challenge, name, host, port) VALUES ('missing', 'http', 'web', 80);`,
		},
		{
			name: "build challenge",
			query: `INSERT INTO builds(
				id, flag, format, seed, hasartifacts, lastsolved,
				challenge, schema, instancecount
			) VALUES (1, 'flag', 'flag{%s}', 1, 0, 0, 'missing', 'schema', 1);`,
		},
		{
			name:  "image build",
			query: `INSERT INTO images(id, build, host) VALUES (1, 999, 'web');`,
		},
		{
			name:  "image port image",
			query: `INSERT INTO imagePorts(image, port) VALUES (999, '80/tcp');`,
		},
		{
			name:  "lookup build",
			query: `INSERT INTO lookupData(build, key, value) VALUES (999, 'key', 'value');`,
		},
		{
			name:  "instance build",
			query: `INSERT INTO instances(id, lastsolved, build) VALUES (1, 0, 999);`,
		},
		{
			name:  "port assignment instance",
			query: `INSERT INTO portAssignments(instance, name, port) VALUES (999, 'http', 30000);`,
		},
		{
			name:  "container instance",
			query: `INSERT INTO containers(instance, id) VALUES (999, 'container');`,
		},
		{
			name: "container options challenge",
			query: `INSERT INTO containerOptions(
				challenge, host, init, cpus, memory, ulimits, pidslimit,
				readonlyrootfs, droppedcaps, nonewprivileges, diskquota,
				cgroupparent
			) VALUES ('missing', '', 0, '', '', '', 0, 0, '', 0, '', '');`,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			requireConstraintFailure(t, manager.db, test.query)
		})
	}
}

func TestDatabaseCascadeAndRestrictActions(t *testing.T) {
	t.Run("challenge identifier updates cascade", func(t *testing.T) {
		manager := newSchemaTestManager(t)
		insertConstraintChallenge(t, manager.db)
		insertChallengeConstraintChildren(t, manager.db)

		requireExec(
			t,
			manager.db,
			"UPDATE challenges SET id='renamed' WHERE id='challenge';",
		)
		requireRowCountWhere(
			t,
			manager.db,
			"challenges",
			"id='renamed'",
			1,
		)
		for _, table := range []string{
			"hints",
			"tags",
			"attributes",
			"hosts",
			"portNames",
			"containerOptions",
		} {
			requireRowCountWhere(
				t,
				manager.db,
				table,
				"challenge='renamed'",
				1,
			)
		}
	})

	t.Run("challenge metadata cascades", func(t *testing.T) {
		manager := newSchemaTestManager(t)
		insertConstraintChallenge(t, manager.db)
		insertChallengeConstraintChildren(t, manager.db)

		requireExec(t, manager.db, "DELETE FROM challenges WHERE id='challenge';")
		for _, table := range []string{
			"hints",
			"tags",
			"attributes",
			"hosts",
			"portNames",
			"containerOptions",
		} {
			requireRowCount(t, manager.db, table, 0)
		}
	})

	t.Run("challenge deletion is restricted by builds", func(t *testing.T) {
		manager := newSchemaTestManager(t)
		insertConstraintChallenge(t, manager.db)
		insertConstraintBuild(t, manager.db)

		requireConstraintFailure(
			t,
			manager.db,
			"DELETE FROM challenges WHERE id='challenge';",
		)
		requireRowCount(t, manager.db, "challenges", 1)
		requireRowCount(t, manager.db, "builds", 1)
	})

	t.Run("challenge identifier update is restricted by builds", func(t *testing.T) {
		manager := newSchemaTestManager(t)
		insertConstraintChallenge(t, manager.db)
		insertConstraintBuild(t, manager.db)

		requireConstraintFailure(
			t,
			manager.db,
			"UPDATE challenges SET id='renamed' WHERE id='challenge';",
		)
		requireRowCountWhere(
			t,
			manager.db,
			"challenges",
			"id='challenge'",
			1,
		)
		requireRowCountWhere(
			t,
			manager.db,
			"builds",
			"challenge='challenge'",
			1,
		)
	})

	t.Run("build children cascade", func(t *testing.T) {
		manager := newSchemaTestManager(t)
		insertConstraintChallenge(t, manager.db)
		insertConstraintBuild(t, manager.db)
		insertConstraintImage(t, manager.db)
		requireExec(
			t,
			manager.db,
			"INSERT INTO imagePorts(image, port) VALUES (1, '80/tcp');",
		)
		requireExec(
			t,
			manager.db,
			"INSERT INTO lookupData(build, key, value) VALUES (1, 'key', 'value');",
		)

		requireExec(t, manager.db, "DELETE FROM builds WHERE id=1;")
		for _, table := range []string{"builds", "images", "imagePorts", "lookupData"} {
			requireRowCount(t, manager.db, table, 0)
		}
		requireRowCount(t, manager.db, "challenges", 1)
	})

	t.Run("build deletion is restricted by instances", func(t *testing.T) {
		manager := newSchemaTestManager(t)
		insertConstraintChallenge(t, manager.db)
		insertConstraintBuild(t, manager.db)
		insertConstraintInstance(t, manager.db)

		requireConstraintFailure(t, manager.db, "DELETE FROM builds WHERE id=1;")
		requireRowCount(t, manager.db, "builds", 1)
		requireRowCount(t, manager.db, "instances", 1)
	})

	t.Run("instance children cascade", func(t *testing.T) {
		manager := newSchemaTestManager(t)
		insertConstraintChallenge(t, manager.db)
		insertConstraintBuild(t, manager.db)
		insertConstraintInstance(t, manager.db)
		requireExec(
			t,
			manager.db,
			"INSERT INTO portAssignments(instance, name, port) VALUES (1, 'http', 30000);",
		)
		requireExec(
			t,
			manager.db,
			"INSERT INTO containers(instance, id) VALUES (1, 'container');",
		)

		requireExec(t, manager.db, "DELETE FROM instances WHERE id=1;")
		for _, table := range []string{"instances", "portAssignments", "containers"} {
			requireRowCount(t, manager.db, table, 0)
		}
		requireRowCount(t, manager.db, "builds", 1)
	})

	t.Run("host deletion cascades to named ports", func(t *testing.T) {
		manager := newSchemaTestManager(t)
		insertConstraintChallenge(t, manager.db)
		insertConstraintHost(t, manager.db)
		requireExec(
			t,
			manager.db,
			"INSERT INTO portNames(challenge, name, host, port) VALUES ('challenge', 'http', 'web', 80);",
		)

		requireExec(
			t,
			manager.db,
			"DELETE FROM hosts WHERE challenge='challenge' AND name='web';",
		)
		requireRowCount(t, manager.db, "hosts", 0)
		requireRowCount(t, manager.db, "portNames", 0)
	})
}

func TestDatabaseDeclaredUniqueConstraints(t *testing.T) {
	manager := newSchemaTestManager(t)
	insertCompleteConstraintFixture(t, manager.db)
	requireExec(
		t,
		manager.db,
		"INSERT INTO hosts(challenge, name, idx, target) VALUES ('challenge', 'worker', 1, 'worker');",
	)
	requireExec(
		t,
		manager.db,
		"INSERT INTO portNames(challenge, name, host, port) VALUES ('challenge', 'admin', 'worker', 80);",
	)
	requireExec(
		t,
		manager.db,
		"INSERT INTO instances(id, lastsolved, build) VALUES (2, 0, 1);",
	)

	tests := []struct {
		name  string
		query string
	}{
		{
			name:  "challenge identifier",
			query: `INSERT INTO challenges SELECT * FROM challenges WHERE id='challenge';`,
		},
		{
			name:  "hint index within challenge",
			query: `INSERT INTO hints(challenge, idx, hint) VALUES ('challenge', 0, 'different');`,
		},
		{
			name:  "tag within challenge",
			query: `INSERT INTO tags(challenge, tag) VALUES ('challenge', 'tag');`,
		},
		{
			name:  "attribute key within challenge",
			query: `INSERT INTO attributes(challenge, key, value) VALUES ('challenge', 'key', 'different');`,
		},
		{
			name:  "host name within challenge",
			query: `INSERT INTO hosts(challenge, name, idx, target) VALUES ('challenge', 'web', 1, 'other');`,
		},
		{
			name:  "host order within challenge",
			query: `INSERT INTO hosts(challenge, name, idx, target) VALUES ('challenge', 'other', 0, 'other');`,
		},
		{
			name:  "logical port name within challenge",
			query: `INSERT INTO portNames(challenge, name, host, port) VALUES ('challenge', 'http', 'worker', 81);`,
		},
		{
			name:  "container port endpoint within challenge",
			query: `INSERT INTO portNames(challenge, name, host, port) VALUES ('challenge', 'https', 'web', 80);`,
		},
		{
			name: "build specification",
			query: `INSERT INTO builds(
				id, flag, format, seed, hasartifacts, lastsolved,
				challenge, schema, instancecount
			) VALUES (2, 'different', 'flag{%s}', 1, 0, 0, 'challenge', 'schema', 1);`,
		},
		{
			name:  "image host within build",
			query: `INSERT INTO images(build, host) VALUES (1, 'web');`,
		},
		{
			name:  "port within image",
			query: `INSERT INTO imagePorts(image, port) VALUES (1, '80/tcp');`,
		},
		{
			name:  "lookup key within build",
			query: `INSERT INTO lookupData(build, key, value) VALUES (1, 'key', 'different');`,
		},
		{
			name:  "logical port assignment within instance",
			query: `INSERT INTO portAssignments(instance, name, port) VALUES (1, 'http', 30001);`,
		},
		{
			name:  "globally assigned host port",
			query: `INSERT INTO portAssignments(instance, name, port) VALUES (2, 'admin', 30000);`,
		},
		{
			name:  "container identifier",
			query: `INSERT INTO containers(instance, id) VALUES (1, 'container');`,
		},
		{
			name: "container options within challenge and host",
			query: `INSERT INTO containerOptions(
				challenge, host, init, cpus, memory, ulimits, pidslimit,
				readonlyrootfs, droppedcaps, nonewprivileges, diskquota,
				cgroupparent
			) VALUES ('challenge', 'web', 0, '', '', '', 0, 0, '', 0, '', '');`,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			requireConstraintFailure(t, manager.db, test.query)
		})
	}
}

func TestReversePortMapDistinguishesHosts(t *testing.T) {
	manager := newSchemaTestManager(t)
	insertConstraintChallenge(t, manager.db)
	insertConstraintHost(t, manager.db)
	requireExec(
		t,
		manager.db,
		"INSERT INTO hosts(challenge, name, idx, target) VALUES ('challenge', 'worker', 1, 'worker');",
	)
	requireExec(
		t,
		manager.db,
		`INSERT INTO portNames(challenge, name, host, port) VALUES
			('challenge', 'http', 'web', 80),
			('challenge', 'admin', 'worker', 80);`,
	)

	actual, err := manager.getReversePortMap("challenge")
	if err != nil {
		t.Fatalf("could not load reverse port map: %s", err)
	}
	expected := map[challengePortEndpoint]string{
		{Host: "web", Port: "80/tcp"}:    "http",
		{Host: "worker", Port: "80/tcp"}: "admin",
	}
	if !reflect.DeepEqual(actual, expected) {
		t.Fatalf("unexpected reverse port map:\ngot:  %#v\nwant: %#v", actual, expected)
	}
}

func TestLookupChallengeMetadataOrdersHosts(t *testing.T) {
	manager := newSchemaTestManager(t)
	insertConstraintChallenge(t, manager.db)
	requireExec(
		t,
		manager.db,
		`INSERT INTO hosts(challenge, name, idx, target) VALUES
			('challenge', 'worker', 1, 'worker'),
			('challenge', 'web', 0, 'web');`,
	)

	metadata, err := manager.lookupChallengeMetadata("challenge")
	if err != nil {
		t.Fatalf("could not load challenge metadata: %s", err)
	}
	expected := []HostInfo{
		{Name: "web", Target: "web"},
		{Name: "worker", Target: "worker"},
	}
	if !reflect.DeepEqual(metadata.Hosts, expected) {
		t.Fatalf("unexpected host order:\ngot:  %#v\nwant: %#v", metadata.Hosts, expected)
	}
}

const challengeConstraintInsert = `INSERT INTO challenges(
	id, name, namespace, challengetype, description, details,
	sourcechecksum, metadatachecksum, path, solvescript, templatable,
	maxusers, category, points
) VALUES (?, ?, '', 'custom', '', '', 0, 0, '/challenge', ?, ?, ?, '', ?);`

const containerOptionsConstraintInsert = `INSERT INTO containerOptions(
	challenge, host, init, cpus, memory, ulimits, pidslimit,
	readonlyrootfs, droppedcaps, nonewprivileges, diskquota, cgroupparent
) VALUES ('challenge', 'web', ?, '', '', '', 0, ?, '', ?, '', '');`

func newSchemaTestManager(t *testing.T) *Manager {
	t.Helper()
	databasePath := filepath.Join(t.TempDir(), "cmgr.db")
	previousPath, hadPreviousPath := os.LookupEnv(DB_ENV)
	if err := os.Setenv(DB_ENV, databasePath); err != nil {
		t.Fatalf("could not configure test database: %s", err)
	}

	manager := &Manager{log: newLogger(DISABLED)}
	if err := manager.initDatabase(); err != nil {
		if hadPreviousPath {
			_ = os.Setenv(DB_ENV, previousPath)
		} else {
			_ = os.Unsetenv(DB_ENV)
		}
		t.Fatalf("could not initialize test database: %s", err)
	}
	manager.db.SetMaxOpenConns(1)
	t.Cleanup(func() {
		_ = manager.db.Close()
		if hadPreviousPath {
			_ = os.Setenv(DB_ENV, previousPath)
		} else {
			_ = os.Unsetenv(DB_ENV)
		}
	})
	return manager
}

func insertCompleteConstraintFixture(t *testing.T, db *sqlx.DB) {
	t.Helper()
	insertConstraintChallenge(t, db)
	insertConstraintHost(t, db)
	insertChallengeConstraintChildren(t, db)
	insertConstraintBuild(t, db)
	insertConstraintImage(t, db)
	insertConstraintInstance(t, db)
	requireExec(
		t,
		db,
		"INSERT INTO imagePorts(image, port) VALUES (1, '80/tcp');",
	)
	requireExec(
		t,
		db,
		"INSERT INTO lookupData(build, key, value) VALUES (1, 'key', 'value');",
	)
	requireExec(
		t,
		db,
		"INSERT INTO portAssignments(instance, name, port) VALUES (1, 'http', 30000);",
	)
	requireExec(
		t,
		db,
		"INSERT INTO containers(instance, id) VALUES (1, 'container');",
	)
}

func insertConstraintChallenge(t *testing.T, db *sqlx.DB) {
	t.Helper()
	requireExec(
		t,
		db,
		challengeConstraintInsert,
		"challenge",
		"Challenge",
		0,
		0,
		0,
		0,
	)
}

func insertConstraintHost(t *testing.T, db *sqlx.DB) {
	t.Helper()
	requireExec(
		t,
		db,
		"INSERT INTO hosts(challenge, name, idx, target) VALUES ('challenge', 'web', 0, 'web');",
	)
}

func insertChallengeConstraintChildren(t *testing.T, db *sqlx.DB) {
	t.Helper()
	requireExec(
		t,
		db,
		"INSERT INTO hints(challenge, idx, hint) VALUES ('challenge', 0, 'hint');",
	)
	requireExec(
		t,
		db,
		"INSERT INTO tags(challenge, tag) VALUES ('challenge', 'tag');",
	)
	requireExec(
		t,
		db,
		"INSERT INTO attributes(challenge, key, value) VALUES ('challenge', 'key', 'value');",
	)
	if countRows(t, db, "hosts") == 0 {
		insertConstraintHost(t, db)
	}
	requireExec(
		t,
		db,
		"INSERT INTO portNames(challenge, name, host, port) VALUES ('challenge', 'http', 'web', 80);",
	)
	requireExec(
		t,
		db,
		`INSERT INTO containerOptions(
			challenge, host, init, cpus, memory, ulimits, pidslimit,
			readonlyrootfs, droppedcaps, nonewprivileges, diskquota,
			cgroupparent
		) VALUES ('challenge', 'web', 0, '', '', '', 0, 0, '', 0, '', '');`,
	)
}

func insertConstraintBuild(t *testing.T, db *sqlx.DB) {
	t.Helper()
	requireExec(
		t,
		db,
		`INSERT INTO builds(
			id, flag, format, seed, hasartifacts, lastsolved,
			challenge, schema, instancecount
		) VALUES (1, 'flag', 'flag{%s}', 1, 0, 0, 'challenge', 'schema', 1);`,
	)
}

func insertConstraintImage(t *testing.T, db *sqlx.DB) {
	t.Helper()
	requireExec(
		t,
		db,
		"INSERT INTO images(id, build, host) VALUES (1, 1, 'web');",
	)
}

func insertConstraintInstance(t *testing.T, db *sqlx.DB) {
	t.Helper()
	requireExec(
		t,
		db,
		"INSERT INTO instances(id, lastsolved, build) VALUES (1, 0, 1);",
	)
}

func requireExec(
	t *testing.T,
	db *sqlx.DB,
	query string,
	args ...interface{},
) {
	t.Helper()
	if _, err := db.Exec(query, args...); err != nil {
		t.Fatalf("database operation failed: %s\nquery: %s", err, query)
	}
}

func requireConstraintFailure(
	t *testing.T,
	db *sqlx.DB,
	query string,
	args ...interface{},
) {
	t.Helper()
	if _, err := db.Exec(query, args...); err == nil {
		t.Fatalf("database accepted an operation that should violate a constraint:\n%s", query)
	}
}

func requireRowCount(t *testing.T, db *sqlx.DB, table string, expected int) {
	t.Helper()
	actual := countRows(t, db, table)
	if actual != expected {
		t.Fatalf(
			"table %s contains %d rows; expected %d",
			table,
			actual,
			expected,
		)
	}
}

func requireRowCountWhere(
	t *testing.T,
	db *sqlx.DB,
	table string,
	predicate string,
	expected int,
) {
	t.Helper()
	var actual int
	query := fmt.Sprintf(
		"SELECT COUNT(*) FROM %s WHERE %s;",
		table,
		predicate,
	)
	if err := db.Get(&actual, query); err != nil {
		t.Fatalf("could not count table %s: %s", table, err)
	}
	if actual != expected {
		t.Fatalf(
			"table %s contains %d matching rows; expected %d",
			table,
			actual,
			expected,
		)
	}
}

func countRows(t *testing.T, db *sqlx.DB, table string) int {
	t.Helper()
	var count int
	query := fmt.Sprintf("SELECT COUNT(*) FROM %s;", table)
	if err := db.Get(&count, query); err != nil {
		t.Fatalf("could not count table %s: %s", table, err)
	}
	return count
}
