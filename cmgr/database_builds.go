package cmgr

import (
	"fmt"
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
        schemaversion,
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
        :schemaversion,
        :instancecount
    ) ON CONFLICT (schema, format, challenge, seed) DO
    UPDATE SET
    	schemaversion = excluded.schemaversion,
    	instancecount = excluded.instancecount;`

func (m *Manager) openBuild(build *BuildMetadata) error {
	res, err := m.db.NamedExec(openBuildQuery, build)
	m.log.debugf("Opening %v", build)

	if err != nil {
		m.log.errorf("failed to open build (%s): %s", build.Challenge, err)
		return err
	}

	buildId, err := res.LastInsertId()
	if err != nil {
		m.log.errorf("failed to get build ID (%s): %s", build.Challenge, err)
		return err
	} else if buildId == 0 {
		m.log.debug("Running select...")
		rows, err := m.db.NamedQuery("SELECT id FROM builds WHERE schema=:schema AND format=:format AND challenge=:challenge AND seed=:seed;", build)
		if err != nil {
			m.log.errorf("failed to find build: %s", err)
		} else if !rows.Next() {
			m.log.error("found no rows when exactly one expected")
		}
		err = rows.Scan(&build.Id)
		if err != nil {
			m.log.errorf("failed to read build ID: %s", err)
		}
		defer rows.Close()
		if rows.Next() {
			m.log.error("found more rows than expected")
		}
	} else {
		build.Id = BuildId(buildId)
	}

	m.log.debugf("Build of %s has ID %d", build.Challenge, build.Id)
	return nil
}

const finalizeBuildQuery string = `
	UPDATE builds
	SET
		flag = :flag,
		hasartifacts = :hasartifacts
	WHERE id = :id;`

func (m *Manager) finalizeBuild(build *BuildMetadata) error {
	txn := m.db.MustBegin()
	res, err := txn.NamedExec(finalizeBuildQuery, build)

	if err != nil {
		m.log.errorf("failed to finalize build (%d): %s", build.Id, err)
		cerr := txn.Rollback()
		if cerr != nil { // If rollback fails, we're in trouble.
			m.log.error(cerr)
			err = cerr
		}
		return err
	}

	rowCount, err := res.RowsAffected()

	if err != nil {
		m.log.errorf("failed to check row count for build (%d): %s", build.Id, err)
		cerr := txn.Rollback()
		if cerr != nil { // If rollback fails, we're in trouble.
			m.log.error(cerr)
			err = cerr
		}
		return err
	}

	if rowCount != 1 {
		err = fmt.Errorf("finalized an unexpected number of builds: finalized %d expected 1", rowCount)
		m.log.error(err)
		cerr := txn.Rollback()
		if cerr != nil { // If rollback fails, we're in trouble.
			m.log.error(cerr)
			err = cerr
		}
		return err
	}

	for k, v := range build.LookupData {
		_, err = txn.Exec("INSERT INTO lookupData(build, key, value) VALUES (?, ?, ?);",
			build.Id,
			k,
			v)

		if err != nil {
			m.log.errorf("failed to finalize lookups for build (%d): %s", build.Id, err)
			cerr := txn.Rollback()
			if cerr != nil { // If rollback fails, we're in trouble.
				m.log.error(cerr)
				err = cerr
			}
			return err
		}
	}

	for _, image := range build.Images {
		res, err := txn.Exec("INSERT INTO images(build, dockerid) VALUES (?, ?);",
			build.Id,
			image.DockerId)
		if err != nil {
			m.log.errorf("failed to finalize images for build (%d/%s): %s", build.Id, image.DockerId, err)
			cerr := txn.Rollback()
			if cerr != nil { // If rollback fails, we're in trouble.
				m.log.error(cerr)
				err = cerr
			}
			return err
		}

		imageId, err := res.LastInsertId()
		if err != nil {
			m.log.error(err)
			cerr := txn.Rollback()
			if cerr != nil { // If rollback fails, we're in trouble.
				m.log.error(cerr)
				err = cerr
			}
			return err
		}

		image.Id = ImageId(imageId)

		for _, port := range image.Ports {
			_, err = txn.Exec("INSERT INTO imagePorts(image, port) VALUES (?, ?);",
				image.Id,
				port)

			if err != nil {
				m.log.errorf("failed to finalize ports for image (%d/%d): %s", build.Id, image.Id, err)
				cerr := txn.Rollback()
				if cerr != nil { // If rollback fails, we're in trouble.
					m.log.error(cerr)
					err = cerr
				}
				return err
			}
		}
	}

	err = txn.Commit()
	if err != nil { // It's undocumented what this means...
		m.log.error(err)
	}

	return err
}

func (m *Manager) removeBuildMetadata(build BuildId) error {
	txn := m.db.MustBegin()
	_, err := txn.Exec("DELETE FROM images WHERE build=?", build)

	if err != nil {
		m.log.errorf("failed to delete images for build (%d): %s", build, err)
		cerr := txn.Rollback()
		if cerr != nil {
			m.log.errorf("rollback failed: %s", cerr)
			err = cerr
		}
		return err
	}

	_, err = txn.Exec("DELETE FROM builds WHERE id=?", build)

	if err != nil {
		m.log.errorf("failed to delete build (%d): %s", build, err)
		cerr := txn.Rollback()
		if cerr != nil {
			m.log.errorf("rollback failed: %s", cerr)
			err = cerr
		}
		return err
	}

	err = txn.Commit()
	if err != nil {
		m.log.errorf("failed to commit deletion of build: %s", err)
	}

	return err
}

func (m *Manager) lookupBuildMetadata(build BuildId) (*BuildMetadata, error) {
	metadata := new(BuildMetadata)
	txn := m.db.MustBegin()

	err := txn.Get(metadata, "SELECT * FROM builds WHERE id=?", build)

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
		err = txn.Select(&metadata.Images, "SELECT id, dockerid FROM images WHERE build=?", build)
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
			m.log.errorf("rollback failed: %s", err)
			err = closeErr
		}
	}

	return metadata, err
}
