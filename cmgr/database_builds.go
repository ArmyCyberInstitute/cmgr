package cmgr

import (
	"errors"
	"fmt"

	"github.com/jmoiron/sqlx"
)

const openBuildQuery string = `
	INSERT INTO builds (
        flag,
        seed,
        format,
        hasartifacts,
        lastsolved,
        challenge,
        schema,
        instancecount
    )
    VALUES (
        :flag,
        :seed,
        :format,
        :hasartifacts,
        :lastsolved,
        :challenge,
        :schema,
        :instancecount
	    ) ON CONFLICT (schema, format, challenge, seed) DO
	    UPDATE SET
		instancecount = excluded.instancecount;`

const stageBuildQuery string = `
	INSERT INTO builds (
		flag,
		seed,
		format,
		hasartifacts,
		lastsolved,
		challenge,
		schema,
		instancecount
	)
	VALUES (
		:flag,
		:seed,
		:format,
		:hasartifacts,
		:lastsolved,
		:challenge,
		:schema,
		:instancecount
	) ON CONFLICT (schema, format, challenge, seed) DO NOTHING;`

func (m *Manager) openBuild(build *BuildMetadata) error {
	_, err := m.db.NamedExec(openBuildQuery, build)
	m.log.debugf("Opening %v", build)

	if err != nil {
		m.log.errorf("failed to open build (%s): %s", build.Challenge, err)
		return err
	}

	m.log.debug("Running select...")
	rows, err := m.db.NamedQuery(
		"SELECT id, flag, hasartifacts, lastsolved FROM builds WHERE schema=:schema AND format=:format AND challenge=:challenge AND seed=:seed;",
		build,
	)
	if err != nil {
		m.log.errorf("failed to find build: %s", err)
		return err
	}
	if !rows.Next() {
		rows.Close()
		err = fmt.Errorf("found no builds when exactly one was expected")
		m.log.error(err)
		return err
	}
	err = rows.Scan(&build.Id, &build.Flag, &build.HasArtifacts, &build.LastSolved)
	if err != nil {
		m.log.errorf("failed to read build ID: %s", err)
		rows.Close()
		return err
	}
	if rows.Next() {
		rows.Close()
		err = fmt.Errorf("found more than one build when exactly one was expected")
		m.log.error(err)
		return err
	}
	if err = rows.Close(); err != nil {
		m.log.errorf("failed to close build query: %s", err)
		return err
	}

	// ON CONFLICT can reopen an already-completed build. Load its related
	// images, lookups, and build-discovered runtime requirements so schema
	// convergence behaves the same as a fresh build.
	if build.Flag != "" {
		persisted, lookupErr := m.lookupBuildMetadata(build.Id)
		if lookupErr != nil {
			return lookupErr
		}
		*build = *persisted
	}

	m.log.debugf("Build of %s has ID %d", build.Challenge, build.Id)
	return nil
}

// stageBuild opens a build without changing the active instance target.
// Newly inserted rows are locked until the entire schema plan is activated.
func (m *Manager) stageBuild(build *BuildMetadata) error {
	requestedCount := build.InstanceCount
	staged := *build
	staged.InstanceCount = LOCKED
	if _, err := m.db.NamedExec(stageBuildQuery, &staged); err != nil {
		return fmt.Errorf("failed to stage build (%s): %w", build.Challenge, err)
	}

	type stagedBuildRow struct {
		Id           BuildId
		Flag         string
		HasArtifacts bool
		LastSolved   int64
	}
	var row stagedBuildRow
	if err := m.db.Get(
		&row,
		`SELECT id, flag, hasartifacts, lastsolved
		 FROM builds
		 WHERE schema=? AND format=? AND challenge=? AND seed=?;`,
		build.Schema,
		build.Format,
		build.Challenge,
		build.Seed,
	); err != nil {
		return fmt.Errorf("could not reload staged build: %w", err)
	}

	build.Id = row.Id
	build.Flag = row.Flag
	build.HasArtifacts = row.HasArtifacts
	build.LastSolved = row.LastSolved
	if build.Flag != "" {
		persisted, err := m.lookupBuildMetadata(build.Id)
		if err != nil {
			return err
		}
		*build = *persisted
	}
	build.InstanceCount = requestedCount
	return nil
}

func (m *Manager) activateSchemaBuilds(
	schema string,
	targets map[BuildId]int,
) error {
	return withTransaction(m.db, func(txn *sqlx.Tx) error {
		if _, err := txn.Exec(
			"UPDATE builds SET instancecount = ? WHERE schema = ?;",
			LOCKED,
			schema,
		); err != nil {
			return fmt.Errorf("could not lock obsolete schema builds: %w", err)
		}
		for build, target := range targets {
			result, err := txn.Exec(
				`UPDATE builds
				 SET instancecount = ?
				 WHERE id = ? AND schema = ?;`,
				target,
				build,
				schema,
			)
			if err != nil {
				return fmt.Errorf("could not activate build %d: %w", build, err)
			}
			affected, err := result.RowsAffected()
			if err != nil {
				return fmt.Errorf("could not inspect activation of build %d: %w", build, err)
			}
			if affected != 1 {
				return fmt.Errorf("could not activate missing build %d", build)
			}
		}
		return nil
	})
}

const finalizeBuildQuery string = `
	UPDATE builds
	SET
		flag = :flag,
		hasartifacts = :hasartifacts,
		requiredseccomptweaks = :requiredseccomptweaks,
		lastsolved = 0
	WHERE id = :id;`

func (m *Manager) finalizeBuild(build *BuildMetadata) error {
	return withTransaction(m.db, func(txn *sqlx.Tx) error {
		result, err := txn.NamedExec(finalizeBuildQuery, build)
		if err != nil {
			return fmt.Errorf("could not update build %d: %w", build.Id, err)
		}
		affected, err := result.RowsAffected()
		if err != nil {
			return fmt.Errorf("could not inspect build %d update: %w", build.Id, err)
		}
		if affected != 1 {
			return fmt.Errorf("finalized %d builds; expected one", affected)
		}
		if _, err := txn.Exec(
			"DELETE FROM lookupData WHERE build=?;",
			build.Id,
		); err != nil {
			return fmt.Errorf("could not clear build lookups: %w", err)
		}
		for key, value := range build.LookupData {
			if _, err := txn.Exec(
				"INSERT INTO lookupData(build, key, value) VALUES (?, ?, ?);",
				build.Id,
				key,
				value,
			); err != nil {
				return fmt.Errorf("could not insert lookup %q: %w", key, err)
			}
		}
		if _, err := txn.Exec(
			"DELETE FROM images WHERE build=?;",
			build.Id,
		); err != nil {
			return fmt.Errorf("could not clear build images: %w", err)
		}
		for imageIndex := range build.Images {
			image := &build.Images[imageIndex]
			result, err := txn.Exec(
				"INSERT INTO images(build, host) VALUES (?, ?);",
				build.Id,
				image.Host,
			)
			if err != nil {
				return fmt.Errorf("could not insert image for host %q: %w", image.Host, err)
			}
			imageID, err := result.LastInsertId()
			if err != nil {
				return fmt.Errorf("could not get image ID for host %q: %w", image.Host, err)
			}
			image.Id = ImageId(imageID)
			for _, port := range image.Ports {
				if _, err := txn.Exec(
					"INSERT INTO imagePorts(image, port) VALUES (?, ?);",
					image.Id,
					port,
				); err != nil {
					return fmt.Errorf(
						"could not insert port %q for host %q: %w",
						port,
						image.Host,
						err,
					)
				}
			}
		}
		return nil
	})
}

func (m *Manager) removeBuildMetadata(build BuildId) error {
	return withTransaction(m.db, func(txn *sqlx.Tx) error {
		result, err := txn.Exec("DELETE FROM builds WHERE id=?", build)
		if err != nil {
			return fmt.Errorf("could not delete build %d: %w", build, err)
		}
		affected, err := result.RowsAffected()
		if err != nil {
			return fmt.Errorf("could not inspect build deletion: %w", err)
		}
		if affected != 1 {
			return unknownBuildIdError(build)
		}
		return nil
	})
}

func (m *Manager) lookupBuildMetadata(build BuildId) (*BuildMetadata, error) {
	metadata := new(BuildMetadata)
	txn, err := m.db.Beginx()
	if err != nil {
		return metadata, fmt.Errorf("could not begin build lookup: %w", err)
	}

	err = txn.Get(metadata, "SELECT * FROM builds WHERE id=?", build)
	if isEmptyQueryError(err) {
		err = unknownBuildIdError(build)
	}

	lookups := []struct {
		Key   string
		Value string
	}{}
	if err == nil {
		err = txn.Select(&lookups, "SELECT key, value FROM lookupData WHERE build=?", build)
	}

	metadata.LookupData = make(map[string]string)
	for _, kvPair := range lookups {
		metadata.LookupData[kvPair.Key] = kvPair.Value
	}

	metadata.Images = []Image{}
	if err == nil {
		err = txn.Select(&metadata.Images, "SELECT id, host FROM images WHERE build=?", build)
		if err == nil {
			for i, image := range metadata.Images {
				err = txn.Select(&metadata.Images[i].Ports, "SELECT port FROM imagePorts WHERE image=?", image.Id)
			}
		}
	}

	if err == nil {
		err = txn.Commit()
		if err != nil {
			m.log.errorf("failed to commit read-only transaction: %s", err)
		}
	} else {
		m.log.errorf("read of database failed: %s", err)
		closeErr := txn.Rollback()
		if closeErr != nil {
			m.log.errorf("rollback failed: %s", closeErr)
			err = errors.Join(err, closeErr)
		}
	}

	return metadata, err
}

func (m *Manager) schemaExists(schema string) (bool, error) {
	var count int
	err := m.db.Get(&count, "SELECT COUNT(*) FROM schemas WHERE name = ?;", schema)
	return count == 1, err
}

func (m *Manager) createSchemaRecord(schema string, manual bool) error {
	_, err := m.db.Exec(
		"INSERT INTO schemas(name, manual) VALUES (?, ?);",
		schema,
		manual,
	)
	if err != nil {
		return fmt.Errorf("could not create schema %q: %w", schema, err)
	}
	return nil
}

func (m *Manager) schemaIsManual(schema string) (bool, error) {
	var manual bool
	err := m.db.Get(&manual, "SELECT manual FROM schemas WHERE name = ?;", schema)
	if isEmptyQueryError(err) {
		return false, unknownSchemaIdError(schema)
	}
	if err != nil {
		return false, fmt.Errorf("could not inspect schema %q: %w", schema, err)
	}
	return manual, nil
}

func (m *Manager) deleteSchemaRecordIfEmpty(schema string) error {
	result, err := m.db.Exec(
		`DELETE FROM schemas
		 WHERE name = ?
		   AND NOT EXISTS (SELECT 1 FROM builds WHERE builds.schema = schemas.name);`,
		schema,
	)
	if err != nil {
		return fmt.Errorf("could not delete empty schema %q: %w", schema, err)
	}
	_, err = result.RowsAffected()
	return err
}

func (m *Manager) deleteSchemaRecord(schema string) error {
	var buildCount int
	if err := m.db.Get(
		&buildCount,
		"SELECT COUNT(*) FROM builds WHERE schema = ?;",
		schema,
	); err != nil {
		return err
	}
	if buildCount != 0 {
		return fmt.Errorf("cannot delete schema %q while %d builds remain", schema, buildCount)
	}
	result, err := m.db.Exec(
		"DELETE FROM schemas WHERE name = ? AND manual = 0;",
		schema,
	)
	if err != nil {
		return err
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if affected != 1 {
		return fmt.Errorf("managed schema %q does not exist", schema)
	}
	return nil
}

func (m *Manager) removedSchemaBuilds(schema string) ([]BuildId, error) {
	builds := []BuildId{}
	err := m.db.Select(&builds, "SELECT id FROM builds WHERE schema = ? AND instancecount = ?;", schema, LOCKED)
	return builds, err
}

func (m *Manager) lockSchema(schema string) error {
	_, err := m.db.Exec("UPDATE builds SET instancecount = ? WHERE schema = ?;", LOCKED, schema)
	return err
}

func (m *Manager) getSchemaBuilds(schema string) ([]BuildId, error) {
	builds := []BuildId{}
	err := m.db.Select(&builds, "SELECT id FROM builds WHERE schema = ? ORDER BY challenge;", schema)
	return builds, err
}

func (m *Manager) queryForSchemas() ([]string, error) {
	schemas := []string{}
	err := m.db.Select(
		&schemas,
		"SELECT name FROM schemas WHERE manual = 0 ORDER BY name;",
	)
	return schemas, err
}
