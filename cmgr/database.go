package cmgr

import (
	"errors"
	"fmt"
	"os"
	"reflect"

	"github.com/jmoiron/sqlx"
	_ "modernc.org/sqlite"
)

const schemaQuery string = `
	CREATE TABLE IF NOT EXISTS challenges (
		id TEXT NOT NULL PRIMARY KEY,
		name TEXT NOT NULL,
		namespace TEXT NOT NULL,
		challengetype TEXT NOT NULL,
		description TEXT NOT NULL,
		details TEXT,
		sourcechecksum INT NOT NULL,
		metadatachecksum INT NOT NULL,
		sourcedigest TEXT NOT NULL DEFAULT '',
		metadatadigest TEXT NOT NULL DEFAULT '',
		path TEXT NOT NULL,
		solvescript INTEGER NOT NULL CHECK(solvescript == 0 OR solvescript == 1),
		templatable INTEGER NOT NULL CHECK(templatable == 0 OR templatable == 1),
		maxusers INTEGER NOT NULL CHECK(maxusers >= 0),
		category TEXT,
		points INTEGER NOT NULL CHECK(points >= 0)
	);

	CREATE TABLE IF NOT EXISTS hints (
		challenge TEXT NOT NULL,
		idx INT NOT NULL,
		hint TEXT NOT NULL,
		PRIMARY KEY (challenge, idx),
		FOREIGN KEY (challenge) REFERENCES challenges (id)
			ON UPDATE CASCADE ON DELETE CASCADE
	);

	CREATE TABLE IF NOT EXISTS tags (
		challenge TEXT NOT NULL,
		tag TEXT NOT NULL,
		PRIMARY KEY (challenge, tag),
		FOREIGN KEY (challenge) REFERENCES challenges (id)
			ON UPDATE CASCADE ON DELETE CASCADE
	);

	CREATE INDEX IF NOT EXISTS tagIndex ON tags(LOWER(tag));

	CREATE TABLE IF NOT EXISTS attributes (
		challenge TEXT NOT NULL,
		key TEXT NOT NULL,
		value TEXT NOT NULL,
		PRIMARY KEY (challenge, key),
		FOREIGN KEY (challenge) REFERENCES challenges (id)
			ON UPDATE CASCADE ON DELETE CASCADE
	);

	CREATE INDEX IF NOT EXISTS attributeIndex ON attributes(LOWER(key));

	CREATE TABLE IF NOT EXISTS hosts (
		challenge TEXT NOT NULL,
		name TEXT NOT NULL,
		idx INT NOT NULL,
		target TEXT NOT NULL,
		PRIMARY KEY (challenge, name),
		FOREIGN KEY (challenge) REFERENCES challenges (id)
		    ON UPDATE CASCADE ON DELETE CASCADE
	);

	CREATE INDEX IF NOT EXISTS hostsIndex ON hosts(challenge);
	CREATE UNIQUE INDEX IF NOT EXISTS hostsOrderIndex
		ON hosts(challenge, idx);

	CREATE TABLE IF NOT EXISTS portNames (
		challenge TEXT NOT NULL,
		name TEXT NOT NULL,
		host TEXT NOT NULL,
		port INTEGER NOT NULL CHECK (port > 0 AND port < 65536),
		FOREIGN KEY (challenge) REFERENCES challenges (id)
			ON UPDATE CASCADE ON DELETE CASCADE,
		FOREIGN KEY (challenge, host) REFERENCES hosts (challenge, name)
		    ON UPDATE CASCADE ON DELETE CASCADE
	);
	CREATE UNIQUE INDEX IF NOT EXISTS portNamesNameIndex
		ON portNames(challenge, name);
	CREATE UNIQUE INDEX IF NOT EXISTS portNamesEndpointIndex
		ON portNames(challenge, host, port);

	CREATE TABLE IF NOT EXISTS schemas (
		name TEXT NOT NULL PRIMARY KEY,
		manual INTEGER NOT NULL CHECK(manual = 0 OR manual = 1)
	);

	CREATE TABLE IF NOT EXISTS builds (
		id INTEGER PRIMARY KEY,
		flag TEXT NOT NULL,
		format TEXT NOT NULL,
		seed INTEGER NOT NULL,
		hasartifacts INTEGER NOT NULL CHECK (hasartifacts = 0 OR hasartifacts = 1),
		lastsolved INTEGER,
		challenge TEXT NOT NULL,
		schema TEXT NOT NULL,
		instancecount INT NOT NULL,
		requiredseccomptweaks TEXT NOT NULL DEFAULT '[]',
		UNIQUE(schema, format, challenge, seed),
		FOREIGN KEY (challenge) REFERENCES challenges (id)
			ON UPDATE RESTRICT ON DELETE RESTRICT
	);

	CREATE INDEX IF NOT EXISTS schemaIndex on builds(schema);

	CREATE TABLE IF NOT EXISTS images (
		id INTEGER PRIMARY KEY,
		build INTEGER NOT NULL,
		host TEXT NOT NULL,
		FOREIGN KEY (build) REFERENCES builds (id)
		    ON UPDATE RESTRICT ON DELETE CASCADE
	);
	CREATE UNIQUE INDEX IF NOT EXISTS imagesHostIndex
		ON images(build, host);

	CREATE TABLE IF NOT EXISTS imagePorts (
		image INTEGER NOT NULL,
		port TEXT NOT NULL,
		FOREIGN KEY (image) REFERENCES images (id)
			ON UPDATE CASCADE ON DELETE CASCADE
	);
	CREATE UNIQUE INDEX IF NOT EXISTS imagePortsPortIndex
		ON imagePorts(image, port);

	CREATE TABLE IF NOT EXISTS lookupData (
		build INTEGER NOT NULL,
		key TEXT NOT NULL,
		value TEXT NOT NULL,
		FOREIGN KEY (build) REFERENCES builds (id)
			ON UPDATE RESTRICT ON DELETE CASCADE
	);
	CREATE UNIQUE INDEX IF NOT EXISTS lookupDataKeyIndex
		ON lookupData(build, key);

	CREATE TABLE IF NOT EXISTS instances (
		id INTEGER PRIMARY KEY,
		lastsolved INTEGER,
		build INTEGER NOT NULL,
		FOREIGN KEY (build) REFERENCES builds (id)
			ON UPDATE RESTRICT ON DELETE RESTRICT
	);

	CREATE TABLE IF NOT EXISTS portAssignments (
		instance INTEGER NOT NULL,
		name TEXT NOT NULL,
		port INTEGER NOT NULL CHECK (port > 0 AND port < 65536),
		FOREIGN KEY (instance) REFERENCES instances (id)
			ON UPDATE RESTRICT ON DELETE CASCADE
	);
	CREATE UNIQUE INDEX IF NOT EXISTS portAssignmentsNameIndex
		ON portAssignments(instance, name);
	CREATE UNIQUE INDEX IF NOT EXISTS portAssignmentsPortIndex
		ON portAssignments(port);

	CREATE TABLE IF NOT EXISTS containers (
		instance INTEGER NOT NULL,
		id TEXT NOT NULL PRIMARY KEY,
		FOREIGN KEY (instance) REFERENCES instances (id)
			ON UPDATE RESTRICT ON DELETE CASCADE
	);

	CREATE TABLE IF NOT EXISTS retiredContainers (
		id TEXT NOT NULL PRIMARY KEY
	);

	CREATE TABLE IF NOT EXISTS retiredNetworks (
		name TEXT NOT NULL PRIMARY KEY
	);

	CREATE TABLE IF NOT EXISTS networkOptions (
		challenge TEXT NOT NULL PRIMARY KEY,
		allowegress INTEGER NOT NULL CHECK(allowegress = 0 OR allowegress = 1),
		FOREIGN KEY (challenge) REFERENCES challenges (id)
			ON UPDATE CASCADE ON DELETE CASCADE
	);

	CREATE TABLE IF NOT EXISTS containerOptions (
		challenge INTEGER NOT NULL,
		host TEXT NOT NULL,
		init INTEGER NOT NULL CHECK(init == 0 OR init == 1),
		cpus TEXT NOT NULL,
		memory TEXT NOT NULL,
		ulimits TEXT NOT NULL,
		pidslimit INTEGER NOT NULL,
		readonlyrootfs INTEGER NOT NULL CHECK(readonlyrootfs == 0 OR readonlyrootfs == 1),
		droppedcaps TEXT NOT NULL,
		nonewprivileges INTEGER NOT NULL CHECK(nonewprivileges == 0 OR nonewprivileges == 1),
		diskquota TEXT NOT NULL,
		cgroupparent TEXT NOT NULL,
		seccomp TEXT NOT NULL DEFAULT '',
		FOREIGN KEY (challenge) REFERENCES challenges (id)
			ON UPDATE CASCADE ON DELETE CASCADE
	);
	CREATE UNIQUE INDEX IF NOT EXISTS containerOptionsHostIndex
		ON containerOptions(challenge, host);`

const (
	currentDatabaseVersion = 2
	sqliteBusyTimeoutMS    = 5000

	databaseV1IndexesQuery = `
		CREATE UNIQUE INDEX IF NOT EXISTS hostsOrderIndex
			ON hosts(challenge, idx);
		CREATE UNIQUE INDEX IF NOT EXISTS portNamesNameIndex
			ON portNames(challenge, name);
		CREATE UNIQUE INDEX IF NOT EXISTS portNamesEndpointIndex
			ON portNames(challenge, host, port);
		CREATE UNIQUE INDEX IF NOT EXISTS imagesHostIndex
			ON images(build, host);
		CREATE UNIQUE INDEX IF NOT EXISTS imagePortsPortIndex
			ON imagePorts(image, port);
		CREATE UNIQUE INDEX IF NOT EXISTS lookupDataKeyIndex
			ON lookupData(build, key);
		CREATE UNIQUE INDEX IF NOT EXISTS portAssignmentsNameIndex
			ON portAssignments(instance, name);
		CREATE UNIQUE INDEX IF NOT EXISTS portAssignmentsPortIndex
			ON portAssignments(port);
		CREATE UNIQUE INDEX IF NOT EXISTS containerOptionsHostIndex
			ON containerOptions(challenge, host);`

	databaseV0ToV1DuplicateCleanupQuery = `
		DELETE FROM portNames
		WHERE rowid NOT IN (
			SELECT MIN(rowid)
			FROM portNames
			GROUP BY challenge, name, host, port
		);

		DELETE FROM imagePorts
		WHERE rowid NOT IN (
			SELECT MIN(rowid)
			FROM imagePorts
			GROUP BY image, port
		);

		DELETE FROM lookupData
		WHERE rowid NOT IN (
			SELECT MIN(rowid)
			FROM lookupData
			GROUP BY build, key, value
		);

		DELETE FROM portAssignments
		WHERE rowid NOT IN (
			SELECT MIN(rowid)
			FROM portAssignments
			GROUP BY instance, name, port
		);

		DELETE FROM containerOptions
		WHERE rowid NOT IN (
			SELECT MIN(rowid)
			FROM containerOptions
			GROUP BY
				challenge,
				host,
				init,
				cpus,
				memory,
				ulimits,
				pidslimit,
				readonlyrootfs,
				droppedcaps,
				nonewprivileges,
				diskquota,
				cgroupparent,
				seccomp
		);`

	databaseV1ToV2Query = `
		CREATE TABLE IF NOT EXISTS schemas (
			name TEXT NOT NULL PRIMARY KEY,
			manual INTEGER NOT NULL CHECK(manual = 0 OR manual = 1)
		);
		INSERT OR IGNORE INTO schemas(name, manual)
		SELECT DISTINCT
			schema,
			CASE WHEN substr(schema, 1, 7) = 'manual-' THEN 1 ELSE 0 END
		FROM builds;

		CREATE TABLE IF NOT EXISTS networkOptions (
			challenge TEXT NOT NULL PRIMARY KEY,
			allowegress INTEGER NOT NULL CHECK(allowegress = 0 OR allowegress = 1),
			FOREIGN KEY (challenge) REFERENCES challenges (id)
				ON UPDATE CASCADE ON DELETE CASCADE
		);
		INSERT OR IGNORE INTO networkOptions(challenge, allowegress)
		SELECT id, 0 FROM challenges;

		CREATE TABLE IF NOT EXISTS retiredContainers (
			id TEXT NOT NULL PRIMARY KEY
		);
		CREATE TABLE IF NOT EXISTS retiredNetworks (
			name TEXT NOT NULL PRIMARY KEY
		);`
)

type databaseMigration struct {
	to    int
	apply func(*sqlx.Tx) error
}

type databaseConflictCheck struct {
	invariant string
	query     string
}

var databaseMigrations = map[int]databaseMigration{
	0: {
		to:    1,
		apply: migrateDatabaseV0ToV1,
	},
	1: {
		to:    2,
		apply: migrateDatabaseV1ToV2,
	},
}

var databaseV1ConflictChecks = []databaseConflictCheck{
	{
		invariant: "unique host ordering per challenge",
		query: `
			SELECT printf('challenge=%Q, idx=%d', challenge, idx)
			FROM hosts
			GROUP BY challenge, idx
			HAVING COUNT(*) > 1
			LIMIT 1;`,
	},
	{
		invariant: "unique published-port names per challenge",
		query: `
			SELECT printf('challenge=%Q, name=%Q', challenge, name)
			FROM portNames
			GROUP BY challenge, name
			HAVING COUNT(*) > 1
			LIMIT 1;`,
	},
	{
		invariant: "unique published endpoints per challenge",
		query: `
			SELECT printf(
				'challenge=%Q, host=%Q, port=%d',
				challenge,
				host,
				port
			)
			FROM portNames
			GROUP BY challenge, host, port
			HAVING COUNT(*) > 1
			LIMIT 1;`,
	},
	{
		invariant: "one image per build host",
		query: `
			SELECT printf('build=%d, host=%Q', build, host)
			FROM images
			GROUP BY build, host
			HAVING COUNT(*) > 1
			LIMIT 1;`,
	},
	{
		invariant: "unique lookup-data keys per build",
		query: `
			SELECT printf('build=%d, key=%Q', build, key)
			FROM lookupData
			GROUP BY build, key
			HAVING COUNT(*) > 1
			LIMIT 1;`,
	},
	{
		invariant: "unique assigned-port names per instance",
		query: `
			SELECT printf('instance=%d, name=%Q', instance, name)
			FROM portAssignments
			GROUP BY instance, name
			HAVING COUNT(*) > 1
			LIMIT 1;`,
	},
	{
		invariant: "globally unique assigned ports",
		query: `
			SELECT printf('port=%d', port)
			FROM portAssignments
			GROUP BY port
			HAVING COUNT(*) > 1
			LIMIT 1;`,
	},
	{
		invariant: "one container-options row per challenge host",
		query: `
			SELECT printf('challenge=%Q, host=%Q', challenge, host)
			FROM containerOptions
			GROUP BY challenge, host
			HAVING COUNT(*) > 1
			LIMIT 1;`,
	},
}

func withTransaction(db *sqlx.DB, apply func(*sqlx.Tx) error) error {
	txn, err := db.Beginx()
	if err != nil {
		return fmt.Errorf("could not begin transaction: %w", err)
	}

	if err := apply(txn); err != nil {
		rollbackErr := txn.Rollback()
		if rollbackErr != nil {
			return errors.Join(
				err,
				fmt.Errorf("could not roll back transaction: %w", rollbackErr),
			)
		}
		return err
	}

	if err := txn.Commit(); err != nil {
		return fmt.Errorf("could not commit transaction: %w", err)
	}
	return nil
}

func setDatabaseVersion(txn *sqlx.Tx, version int) error {
	_, err := txn.Exec(fmt.Sprintf("PRAGMA user_version = %d;", version))
	if err != nil {
		return fmt.Errorf("could not set database version to %d: %w", version, err)
	}
	return nil
}

func createCurrentDatabaseSchema(db *sqlx.DB) error {
	return withTransaction(db, func(txn *sqlx.Tx) error {
		if _, err := txn.Exec(schemaQuery); err != nil {
			return fmt.Errorf("could not create database schema: %w", err)
		}
		return setDatabaseVersion(txn, currentDatabaseVersion)
	})
}

func validateDatabaseV0Schema(txn *sqlx.Tx) error {
	const expectedTableCount = 14

	var tableCount int
	err := txn.Get(
		&tableCount,
		`SELECT COUNT(*)
		 FROM sqlite_schema
		 WHERE type = 'table'
		   AND name IN (
				'challenges',
				'hints',
				'tags',
				'attributes',
				'hosts',
				'portNames',
				'builds',
				'images',
				'imagePorts',
				'lookupData',
				'instances',
				'portAssignments',
				'containers',
				'containerOptions'
		   );`,
	)
	if err != nil {
		return fmt.Errorf("could not inspect version 0 database tables: %w", err)
	}
	if tableCount != expectedTableCount {
		return fmt.Errorf(
			"unsupported version 0 database schema: found %d of %d required tables",
			tableCount,
			expectedTableCount,
		)
	}
	return nil
}

func addDatabaseColumnIfMissing(
	txn *sqlx.Tx,
	table string,
	column string,
	inspectionQuery string,
	alterQuery string,
) error {
	var columnCount int
	if err := txn.Get(&columnCount, inspectionQuery); err != nil {
		return fmt.Errorf("could not inspect %s.%s: %w", table, column, err)
	}

	switch columnCount {
	case 0:
		if _, err := txn.Exec(alterQuery); err != nil {
			return fmt.Errorf("could not add %s.%s: %w", table, column, err)
		}
	case 1:
		return nil
	default:
		return fmt.Errorf(
			"invalid version 0 database schema: %s.%s appears %d times",
			table,
			column,
			columnCount,
		)
	}
	return nil
}

func rejectDatabaseV1Conflicts(txn *sqlx.Tx) error {
	for _, check := range databaseV1ConflictChecks {
		var conflicts []string
		if err := txn.Select(&conflicts, check.query); err != nil {
			return fmt.Errorf(
				"could not validate %s: %w",
				check.invariant,
				err,
			)
		}
		if len(conflicts) != 0 {
			return fmt.Errorf(
				"cannot migrate database: %s conflict (%s)",
				check.invariant,
				conflicts[0],
			)
		}
	}
	return nil
}

func migrateDatabaseV0ToV1(txn *sqlx.Tx) error {
	if err := validateDatabaseV0Schema(txn); err != nil {
		return err
	}

	if err := addDatabaseColumnIfMissing(
		txn,
		"containerOptions",
		"seccomp",
		"SELECT COUNT(*) FROM pragma_table_info('containerOptions') WHERE name = 'seccomp';",
		"ALTER TABLE containerOptions ADD COLUMN seccomp TEXT NOT NULL DEFAULT '';",
	); err != nil {
		return err
	}
	if err := addDatabaseColumnIfMissing(
		txn,
		"builds",
		"requiredseccomptweaks",
		"SELECT COUNT(*) FROM pragma_table_info('builds') WHERE name = 'requiredseccomptweaks';",
		"ALTER TABLE builds ADD COLUMN requiredseccomptweaks TEXT NOT NULL DEFAULT '[]';",
	); err != nil {
		return err
	}

	if _, err := txn.Exec(databaseV0ToV1DuplicateCleanupQuery); err != nil {
		return fmt.Errorf("could not remove exact duplicate rows: %w", err)
	}
	if err := rejectDatabaseV1Conflicts(txn); err != nil {
		return err
	}
	if _, err := txn.Exec(databaseV1IndexesQuery); err != nil {
		return fmt.Errorf("could not create version 1 database indexes: %w", err)
	}
	return nil
}

func migrateDatabaseV1ToV2(txn *sqlx.Tx) error {
	if err := addDatabaseColumnIfMissing(
		txn,
		"challenges",
		"sourcedigest",
		"SELECT COUNT(*) FROM pragma_table_info('challenges') WHERE name = 'sourcedigest';",
		"ALTER TABLE challenges ADD COLUMN sourcedigest TEXT NOT NULL DEFAULT '';",
	); err != nil {
		return err
	}
	if err := addDatabaseColumnIfMissing(
		txn,
		"challenges",
		"metadatadigest",
		"SELECT COUNT(*) FROM pragma_table_info('challenges') WHERE name = 'metadatadigest';",
		"ALTER TABLE challenges ADD COLUMN metadatadigest TEXT NOT NULL DEFAULT '';",
	); err != nil {
		return err
	}
	if _, err := txn.Exec(databaseV1ToV2Query); err != nil {
		return fmt.Errorf("could not create version 2 database objects: %w", err)
	}
	return nil
}

var currentDatabaseColumns = map[string][]string{
	"challenges": {
		"id", "name", "namespace", "challengetype", "description", "details",
		"sourcechecksum", "metadatachecksum", "sourcedigest", "metadatadigest",
		"path", "solvescript", "templatable", "maxusers", "category", "points",
	},
	"hints":             {"challenge", "idx", "hint"},
	"tags":              {"challenge", "tag"},
	"attributes":        {"challenge", "key", "value"},
	"hosts":             {"challenge", "name", "idx", "target"},
	"portNames":         {"challenge", "name", "host", "port"},
	"schemas":           {"name", "manual"},
	"builds":            {"id", "flag", "format", "seed", "hasartifacts", "lastsolved", "challenge", "schema", "instancecount", "requiredseccomptweaks"},
	"images":            {"id", "build", "host"},
	"imagePorts":        {"image", "port"},
	"lookupData":        {"build", "key", "value"},
	"instances":         {"id", "lastsolved", "build"},
	"portAssignments":   {"instance", "name", "port"},
	"containers":        {"instance", "id"},
	"retiredContainers": {"id"},
	"retiredNetworks":   {"name"},
	"networkOptions":    {"challenge", "allowegress"},
	"containerOptions": {
		"challenge", "host", "init", "cpus", "memory", "ulimits", "pidslimit",
		"readonlyrootfs", "droppedcaps", "nonewprivileges", "diskquota",
		"cgroupparent", "seccomp",
	},
}

var currentDatabaseIndexes = []string{
	"tagIndex",
	"attributeIndex",
	"hostsIndex",
	"hostsOrderIndex",
	"portNamesNameIndex",
	"portNamesEndpointIndex",
	"schemaIndex",
	"imagesHostIndex",
	"imagePortsPortIndex",
	"lookupDataKeyIndex",
	"portAssignmentsNameIndex",
	"portAssignmentsPortIndex",
	"containerOptionsHostIndex",
}

var currentDatabaseInvariants = []databaseConflictCheck{
	{
		invariant: "every build belongs to a defined schema",
		query: `
			SELECT printf('build=%d, schema=%Q', builds.id, builds.schema)
			FROM builds
			LEFT JOIN schemas ON schemas.name = builds.schema
			WHERE schemas.name IS NULL
			LIMIT 1;`,
	},
	{
		invariant: "every challenge has network options",
		query: `
			SELECT printf('challenge=%Q', challenges.id)
			FROM challenges
			LEFT JOIN networkOptions
				ON networkOptions.challenge = challenges.id
			WHERE networkOptions.challenge IS NULL
			LIMIT 1;`,
	},
	{
		invariant: "active containers are not pending cleanup",
		query: `
			SELECT printf('container=%Q', containers.id)
			FROM containers
			JOIN retiredContainers ON retiredContainers.id = containers.id
			LIMIT 1;`,
	},
}

func validateCurrentDatabaseSchema(db *sqlx.DB) error {
	for table, requiredColumns := range currentDatabaseColumns {
		var tableCount int
		if err := db.Get(
			&tableCount,
			"SELECT COUNT(*) FROM sqlite_schema WHERE type='table' AND name=?;",
			table,
		); err != nil {
			return fmt.Errorf("could not inspect table %s: %w", table, err)
		}
		if tableCount != 1 {
			return fmt.Errorf("database version %d is missing required table %q", currentDatabaseVersion, table)
		}

		type columnInfo struct {
			Name string
		}
		columns := []columnInfo{}
		if err := db.Select(
			&columns,
			fmt.Sprintf("SELECT name FROM pragma_table_info(%q);", table),
		); err != nil {
			return fmt.Errorf("could not inspect columns for table %s: %w", table, err)
		}
		present := make(map[string]struct{}, len(columns))
		for _, column := range columns {
			present[column.Name] = struct{}{}
		}
		for _, column := range requiredColumns {
			if _, ok := present[column]; !ok {
				return fmt.Errorf(
					"database version %d is missing required column %s.%s",
					currentDatabaseVersion,
					table,
					column,
				)
			}
		}
	}

	for _, index := range currentDatabaseIndexes {
		var indexCount int
		if err := db.Get(
			&indexCount,
			"SELECT COUNT(*) FROM sqlite_schema WHERE type='index' AND name=?;",
			index,
		); err != nil {
			return fmt.Errorf("could not inspect index %s: %w", index, err)
		}
		if indexCount != 1 {
			return fmt.Errorf("database version %d is missing required index %q", currentDatabaseVersion, index)
		}
	}

	rows, err := db.Queryx("PRAGMA foreign_key_check;")
	if err != nil {
		return fmt.Errorf("could not check database foreign keys: %w", err)
	}
	defer rows.Close()
	if rows.Next() {
		return errors.New("database contains foreign-key violations")
	}
	if err := rows.Err(); err != nil {
		return fmt.Errorf("could not check database foreign keys: %w", err)
	}
	for _, check := range currentDatabaseInvariants {
		var conflicts []string
		if err := db.Select(&conflicts, check.query); err != nil {
			return fmt.Errorf("could not validate %s: %w", check.invariant, err)
		}
		if len(conflicts) != 0 {
			return fmt.Errorf(
				"database violates invariant %q (%s)",
				check.invariant,
				conflicts[0],
			)
		}
	}
	return nil
}

func migrateDatabase(db *sqlx.DB, fromVersion int) error {
	version := fromVersion
	for version < currentDatabaseVersion {
		migration, ok := databaseMigrations[version]
		if !ok || migration.to <= version {
			return fmt.Errorf("no valid database migration from version %d", version)
		}

		err := withTransaction(db, func(txn *sqlx.Tx) error {
			if err := migration.apply(txn); err != nil {
				return fmt.Errorf(
					"could not migrate database from version %d to %d: %w",
					version,
					migration.to,
					err,
				)
			}
			return setDatabaseVersion(txn, migration.to)
		})
		if err != nil {
			return err
		}
		version = migration.to
	}
	return nil
}

func ensureDatabaseSchema(db *sqlx.DB) error {
	var version int
	if err := db.Get(&version, "PRAGMA user_version;"); err != nil {
		return fmt.Errorf("could not read database version: %w", err)
	}
	if version > currentDatabaseVersion {
		return fmt.Errorf(
			"database version %d is newer than supported version %d",
			version,
			currentDatabaseVersion,
		)
	}

	var tableCount int
	if err := db.Get(
		&tableCount,
		`SELECT COUNT(*)
		 FROM sqlite_schema
		 WHERE type = 'table' AND name NOT LIKE 'sqlite_%';`,
	); err != nil {
		return fmt.Errorf("could not inspect database tables: %w", err)
	}

	if tableCount == 0 {
		if version != 0 {
			return fmt.Errorf(
				"database version is %d but the database contains no tables",
				version,
			)
		}
		if err := createCurrentDatabaseSchema(db); err != nil {
			return err
		}
		return validateCurrentDatabaseSchema(db)
	}
	if version == currentDatabaseVersion {
		return validateCurrentDatabaseSchema(db)
	}
	if err := migrateDatabase(db, version); err != nil {
		return err
	}
	return validateCurrentDatabaseSchema(db)
}

// Connects to the desired database (creating it if it does not exist) and then
// creates or migrates its schema and ensures that the sqlite engine is
// enforcing foreign key constraints.
func (m *Manager) initDatabase() error {
	dbPath, isSet := os.LookupEnv(DB_ENV)
	if !isSet {
		dbPath = "cmgr.db"
	}

	dataSourceName := fmt.Sprintf(
		"%s?_pragma=foreign_keys(1)&_pragma=busy_timeout(%d)",
		dbPath,
		sqliteBusyTimeoutMS,
	)
	db, err := sqlx.Open("sqlite", dataSourceName)
	if err != nil {
		m.log.errorf("could not open database: %s", err)
		return err
	}

	initialized := false
	defer func() {
		if !initialized {
			_ = db.Close()
		}
	}()

	var fkeysEnforced bool
	err = db.QueryRow("PRAGMA foreign_keys;").Scan(&fkeysEnforced)
	if err != nil {
		m.log.errorf("could not check for foreign key support: %s", err)
		return err
	}

	if !fkeysEnforced {
		m.log.errorf("foreign keys not enabled")
		return errors.New("foreign keys not enabled")
	}

	var busyTimeoutMS int
	err = db.QueryRow("PRAGMA busy_timeout;").Scan(&busyTimeoutMS)
	if err != nil {
		m.log.errorf("could not check SQLite busy timeout: %s", err)
		return err
	}
	if busyTimeoutMS != sqliteBusyTimeoutMS {
		return fmt.Errorf(
			"SQLite busy timeout is %dms; expected %dms",
			busyTimeoutMS,
			sqliteBusyTimeoutMS,
		)
	}

	if err = ensureDatabaseSchema(db); err != nil {
		m.log.errorf("could not set database schema: %s", err)
		return err
	}

	m.dbPath = dbPath
	m.db = db
	initialized = true

	return nil
}

type challengePortEndpoint struct {
	Host string
	Port string
}

func (m *Manager) getReversePortMap(id ChallengeId) (map[challengePortEndpoint]string, error) {
	rpm := make(map[challengePortEndpoint]string)

	res := []struct {
		Name string
		Host string
		Port int
	}{}

	err := m.db.Select(&res, `SELECT name, host, port FROM portNames WHERE challenge=?;`, id)
	if err != nil {
		m.log.errorf("could not get challenge ports: %s", err)
		return nil, err
	}

	for _, entry := range res {
		endpoint := challengePortEndpoint{
			Host: entry.Host,
			Port: fmt.Sprintf("%d/tcp", entry.Port),
		}
		rpm[endpoint] = entry.Name
	}

	m.log.debugf("reverse port map for %s: %v", id, rpm)

	return rpm, nil
}

func (m *Manager) usedPortSet() (map[int]struct{}, error) {
	var ports []int
	err := m.db.Select(&ports, "SELECT port FROM portAssignments;")

	portSet := make(map[int]struct{})
	for _, port := range ports {
		portSet[port] = struct{}{}
	}

	return portSet, err
}

func (m *Manager) safeToRefresh(new *ChallengeMetadata) bool {
	old, err := m.lookupChallengeMetadata(new.Id)
	if err != nil {
		return false
	}

	sameType := old.ChallengeType == new.ChallengeType
	sameOptions := reflect.DeepEqual(old.ChallengeOptions, new.ChallengeOptions)

	// Hacksport metadata is also build input: it supplies class attributes,
	// package dependencies, flag-generation context, and artifact templates.
	// Treat any metadata edit as requiring a rebuild rather than a database-only
	// refresh.
	safe := sameType &&
		sameOptions &&
		!isHacksportChallengeType(new.ChallengeType)

	return safe
}

func (m *Manager) dumpState() ([]*ChallengeMetadata, error) {
	challenges, err := m.listChallenges()
	if err != nil {
		return nil, err
	}

	for i, challenge := range challenges {
		meta, err := m.lookupChallengeMetadata(challenge.Id)
		if err != nil {
			return nil, err
		}

		meta.Builds = []*BuildMetadata{}
		err = m.db.Select(&meta.Builds, "SELECT id FROM builds WHERE challenge=?", challenge.Id)
		if err != nil {
			m.log.errorf("failed to select builds for '%s': %s", challenge.Id, err)
			return nil, err
		}

		for j, build := range meta.Builds {
			bMeta, err := m.lookupBuildMetadata(build.Id)
			if err != nil {
				return nil, err
			}

			bMeta.Instances = []*InstanceMetadata{}
			err = m.db.Select(&bMeta.Instances, "SELECT id FROM instances WHERE build=?", bMeta.Id)
			if err != nil {
				m.log.errorf("failed to select instances for '%s/%d': %s", challenge.Id, bMeta.Id, err)
				return nil, err
			}

			for k, instance := range bMeta.Instances {
				iMeta, err := m.lookupInstanceMetadata(instance.Id)
				if err != nil {
					return nil, err
				}

				bMeta.Instances[k] = iMeta
			}
			meta.Builds[j] = bMeta
		}
		challenges[i] = meta
	}
	return challenges, nil
}
