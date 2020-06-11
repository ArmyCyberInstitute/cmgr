package cmgr

import (
	"errors"
	"os"

	"github.com/jmoiron/sqlx"
	_ "github.com/mattn/go-sqlite3"
)

// Connects to the desired database (creating it if it does not exist) and then
// ensures that the necessary tables and indexes exist and that the sqlite
// engine is enforcing foreign key constraints.
func (m *Manager) initDatabase() error {
	dbPath, isSet := os.LookupEnv(DB_ENV)
	if !isSet {
		dbPath = "cmgr.db"
	}

	db, err := sqlx.Open("sqlite3", dbPath+"?_fk=true")
	if err != nil {
		m.log.errorf("could not open database: %s", err)
		return err
	}

	// File exists and is a valid sqlite database
	m.dbPath = dbPath

	_, err = db.Exec(schemaQuery)
	if err != nil {
		m.log.errorf("could not set database schema: %s", err)
		return err
	}

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

	m.db = db

	return nil
}

// Gets just the ID and checksum for all known challenges
func (m *Manager) listChallenges() ([]*ChallengeMetadata, error) {
	metadata := []*ChallengeMetadata{}
	err := m.db.Select(&metadata, "SELECT id, checksum FROM challenges;")
	return metadata, err
}

// Adds the discovered challenges to the database
func (m *Manager) addChallenges(addedChallenges []*ChallengeMetadata) []error {
	errs := []error{}
	for _, metadata := range addedChallenges {
		txn := m.db.MustBegin()

		_, err := txn.NamedExec(challengeInsertQuery, metadata)
		if err != nil {
			m.log.error(err)
			err = txn.Rollback()
			if err != nil { // If rollback fails, we're in trouble.
				m.log.error(err)
				return append(errs, err)
			}
			continue
		}

		for _, hint := range metadata.Hints {
			_, err = txn.Exec("INSERT INTO hints(challenge, hint) VALUES (?, ?);",
				metadata.Id,
				hint)

			if err != nil {
				m.log.error(err)
				err = txn.Rollback()
				if err != nil { // If rollback fails, we're in trouble.
					m.log.error(err)
					return append(errs, err)
				}
				continue
			}
		}

		for _, tag := range metadata.Tags {
			_, err = txn.Exec("INSERT INTO tags(challenge, tag) VALUES (?, ?);",
				metadata.Id,
				tag)

			if err != nil {
				m.log.error(err)
				err = txn.Rollback()
				if err != nil { // If rollback fails, we're in trouble.
					m.log.error(err)
					return append(errs, err)
				}
				continue
			}
		}

		for k, v := range metadata.Attributes {
			_, err = txn.Exec("INSERT INTO attributes(challenge, key, value) VALUES (?, ?, ?);",
				metadata.Id,
				k,
				v)

			if err != nil {
				m.log.error(err)
				err = txn.Rollback()
				if err != nil { // If rollback fails, we're in trouble.
					m.log.error(err)
					return append(errs, err)
				}
				continue
			}
		}

		if err := txn.Commit(); err != nil { // It's undocumented what this means...
			m.log.error(err)
			errs = append(errs, err)
		}
	}
	return errs
}

func (m *Manager) updateChallenges(updatedChallenges []*ChallengeMetadata) []error {
	errs := []error{}
	for _, metadata := range updatedChallenges {
		txn := m.db.MustBegin()

		_, err := txn.NamedExec(challengeUpdateQuery, metadata)
		if err != nil {
			m.log.error(err)
			err = txn.Rollback()
			if err != nil { // If rollback fails, we're in trouble.
				m.log.error(err)
				return append(errs, err)
			}
			continue
		}

		for _, hint := range metadata.Hints {
			_, err = txn.Exec("DELETE FROM hints WHERE challenge = ?;", metadata.Id)

			if err != nil {
				m.log.error(err)
				err = txn.Rollback()
				if err != nil { // If rollback fails, we're in trouble.
					m.log.error(err)
					return append(errs, err)
				}
				continue
			}

			_, err = txn.Exec("INSERT INTO hints(challenge, hint) VALUES (?, ?);",
				metadata.Id,
				hint)

			if err != nil {
				m.log.error(err)
				err = txn.Rollback()
				if err != nil { // If rollback fails, we're in trouble.
					m.log.error(err)
					return append(errs, err)
				}
				continue
			}
		}

		for _, tag := range metadata.Tags {
			_, err = txn.Exec("DELETE FROM tags WHERE challenge = ?;", metadata.Id)

			if err != nil {
				m.log.error(err)
				err = txn.Rollback()
				if err != nil { // If rollback fails, we're in trouble.
					m.log.error(err)
					return append(errs, err)
				}
				continue
			}

			_, err = txn.Exec("INSERT INTO tags(challenge, tag) VALUES (?, ?);",
				metadata.Id,
				tag)

			if err != nil {
				m.log.error(err)
				err = txn.Rollback()
				if err != nil { // If rollback fails, we're in trouble.
					m.log.error(err)
					return append(errs, err)
				}
				continue
			}
		}

		for k, v := range metadata.Attributes {
			_, err = txn.Exec("DELETE FROM attributes WHERE challenge = ?;", metadata.Id)

			if err != nil {
				m.log.error(err)
				err = txn.Rollback()
				if err != nil { // If rollback fails, we're in trouble.
					m.log.error(err)
					return append(errs, err)
				}
				continue
			}

			_, err = txn.Exec("INSERT INTO attributes(challenge, key, value) VALUES (?, ?, ?);",
				metadata.Id,
				k,
				v)

			if err != nil {
				m.log.error(err)
				err = txn.Rollback()
				if err != nil { // If rollback fails, we're in trouble.
					m.log.error(err)
					return append(errs, err)
				}
				continue
			}
		}

		if err := txn.Commit(); err != nil { // It's undocumented what this means...
			m.log.error(err)
			errs = append(errs, err)
		}
	}
	return errs
}

func (m *Manager) removeChallenges(removedChallenges []*ChallengeMetadata) error {
	txn := m.db.MustBegin()
	for _, metadata := range removedChallenges {
		_, err := txn.Exec("DELETE FROM attributes WHERE challenge = ?;", metadata.Id)
		if err != nil {
			m.log.error(err)
			rbErr := txn.Rollback()
			if rbErr != nil { // If rollback fails, we're in trouble.
				m.log.error(rbErr)
				return rbErr
			}
			return err
		}

		_, err = txn.Exec("DELETE FROM tags WHERE challenge = ?;", metadata.Id)
		if err != nil {
			m.log.error(err)
			rbErr := txn.Rollback()
			if rbErr != nil { // If rollback fails, we're in trouble.
				m.log.error(rbErr)
				return rbErr
			}
			return err
		}

		_, err = txn.Exec("DELETE FROM hints WHERE challenge = ?;", metadata.Id)
		if err != nil {
			m.log.error(err)
			rbErr := txn.Rollback()
			if rbErr != nil { // If rollback fails, we're in trouble.
				m.log.error(rbErr)
				return rbErr
			}
			return err
		}

		// This should throw an error and cause a rollback when builds exist for
		// a challenge we are removed.
		_, err = txn.Exec("DELETE FROM challenges WHERE id = ?;", metadata.Id)
		if err != nil {
			m.log.error(err)
			rbErr := txn.Rollback()
			if rbErr != nil { // If rollback fails, we're in trouble.
				m.log.error(rbErr)
				return rbErr
			}
			return err
		}
	}

	if err := txn.Commit(); err != nil { // It's undocumented what this means...
		m.log.error(err)
		return err
	}
	return nil
}

const (
	// Query that can be unconditionally run to ensure the database is initialized
	// upon first connecting.
	schemaQuery string = `
	CREATE TABLE IF NOT EXISTS challenges (
		id TEXT NOT NULL PRIMARY KEY,
		name TEXT NOT NULL,
		namespace TEXT NOT NULL,
		challengetype TEXT NOT NULL,
		description TEXT NOT NULL,
		details TEXT,
		checksum INT NOT NULL,
		path TEXT NOT NULL,
		solvescript INTEGER NOT NULL CHECK(solvescript == 0 OR solvescript == 1),
		templatable INTEGER NOT NULL CHECK(templatable == 0 OR templatable == 1),
		maxusers INTEGER NOT NULL CHECK(maxusers >= 0),
		category TEXT,
		points INTEGER NOT NULL CHECK(points >= 0)
	);

	CREATE TABLE IF NOT EXISTS hints (
		challenge TEXT NOT NULL,
		hint TEXT NOT NULL,
		FOREIGN KEY (challenge) REFERENCES challenges (id)
			ON UPDATE RESTRICT ON DELETE RESTRICT
	);

	CREATE TABLE IF NOT EXISTS tags (
		challenge TEXT NOT NULL,
		tag TEXT NOT NULL,
		FOREIGN KEY (challenge) REFERENCES challenges (id)
			ON UPDATE RESTRICT ON DELETE RESTRICT
	);

	CREATE INDEX IF NOT EXISTS tagIndex ON tags(LOWER(tag));

	CREATE TABLE IF NOT EXISTS attributes (
		challenge TEXT NOT NULL,
		key TEXT NOT NULL,
		value TEXT NOT NULL,
		FOREIGN KEY (challenge) REFERENCES challenges (id)
			ON UPDATE RESTRICT ON DELETE RESTRICT
	);

	CREATE INDEX IF NOT EXISTS attributeIndex ON attributes(LOWER(key));

	CREATE TABLE IF NOT EXISTS portNames (
		id INTEGER PRIMARY KEY,
		challenge TEXT NOT NULL,
		name TEXT NOT NULL,
		service TEXT NOT NULL,
		port INTEGER NOT NULL CHECK (port > 0 AND port < 65536),
		FOREIGN KEY (challenge) REFERENCES challenges (id)
			ON UPDATE RESTRICT ON DELETE RESTRICT
	);

	CREATE TABLE IF NOT EXISTS builds (
		id INTEGER PRIMARY KEY,
		flag TEXT NOT NULL,
		seed TEXT NOT NULL,
		lastsolved INTEGER,
		challenge TEXT NOT NULL,
		FOREIGN KEY (challenge) REFERENCES challenges (id)
			ON UPDATE RESTRICT ON DELETE RESTRICT
	);

	CREATE TABLE IF NOT EXISTS lookupData (
		build INTEGER NOT NULL,
		key TEXT NOT NULL,
		value TEXT NOT NULL,
		FOREIGN KEY (build) REFERENCES builds (id)
			ON UPDATE RESTRICT ON DELETE RESTRICT
	);

	CREATE TABLE IF NOT EXISTS instances (
		id INTEGER PRIMARY KEY,
		lastsolved INTEGER,
		build INTEGER NOT NULL,
		FOREIGN KEY (build) REFERENCES builds (id)
			ON UPDATE RESTRICT ON DELETE RESTRICT
	);

	CREATE TABLE IF NOT EXISTS portAssignments (
		instance INTEGER NOT NULL,
		nameid INTEGER NOT NULL,
		port INTEGER NOT NULL CHECK (port > 0 AND port < 65536),
		FOREIGN KEY (instance) REFERENCES instances (id)
			ON UPDATE RESTRICT ON DELETE RESTRICT,
		FOREIGN KEY (nameid) REFERENCES portNames (id)
			ON UPDATE RESTRICT ON DELETE RESTRICT
	);`

	challengeInsertQuery string = `
	INSERT INTO challenges (
		id,
		name,
		namespace,
		challengetype,
		description,
		details,
		checksum,
		path,
		solvescript,
		templatable,
		maxusers,
		category,
		points
	)
	VALUES (
		:id,
		:name,
		:namespace,
		:challengetype,
		:description,
		:details,
		:checksum,
		:path,
		:solvescript,
		:templatable,
		:maxusers,
		:category,
		:points
	);`

	challengeUpdateQuery string = `
	UPDATE challenges SET
		challengetype = :challengetype,
		description = :description,
		details = :details,
		checksum = :checksum,
		path = :path,
		solvescript = :solvescript,
		templatable = :templatable,
		maxusers = :maxusers,
		category = :category,
		points = :points
	WHERE id = :id;`
)
