package cmgr

import (
	"errors"
	"fmt"

	"github.com/jmoiron/sqlx"
)

func (m *Manager) openInstance(meta *InstanceMetadata) error {
	res, err := m.db.NamedExec("INSERT INTO instances(build, lastsolved) VALUES (:build, :lastsolved);", meta)

	if err != nil {
		m.log.errorf("failed to create instance entry: %s", err)
		return err
	}

	id, err := res.LastInsertId()
	if err != nil {
		m.log.errorf("failed to get instance id: %s", err)
		return err
	}

	meta.Id = InstanceId(id)
	return nil
}

func (m *Manager) finalizeInstance(meta *InstanceMetadata) error {
	return withTransaction(m.db, func(txn *sqlx.Tx) error {
		for name, port := range meta.Ports {
			if _, err := txn.Exec(
				"INSERT INTO portAssignments(instance, name, port) VALUES (?, ?, ?);",
				meta.Id,
				name,
				port,
			); err != nil {
				return fmt.Errorf("could not record port %q: %w", name, err)
			}
		}
		for _, containerID := range meta.Containers {
			if _, err := txn.Exec(
				"DELETE FROM retiredContainers WHERE id = ?;",
				containerID,
			); err != nil {
				return fmt.Errorf("could not clear retired container %s: %w", containerID, err)
			}
			if _, err := txn.Exec(
				"INSERT INTO containers(instance, id) VALUES (?, ?);",
				meta.Id,
				containerID,
			); err != nil {
				return fmt.Errorf("could not record container %s: %w", containerID, err)
			}
		}
		return nil
	})
}

// replaceInstanceRuntimeMetadata atomically swaps the ephemeral container IDs
// and host-port assignments for an existing instance. It does not change the
// instance row or the challenge's logical port definitions in portNames.
func (m *Manager) replaceInstanceRuntimeMetadata(meta *InstanceMetadata) error {
	return withTransaction(m.db, func(txn *sqlx.Tx) error {
		var previous []string
		if err := txn.Select(
			&previous,
			"SELECT id FROM containers WHERE instance=?;",
			meta.Id,
		); err != nil {
			return err
		}
		if _, err := txn.Exec(
			"DELETE FROM portAssignments WHERE instance=?;",
			meta.Id,
		); err != nil {
			return err
		}
		if _, err := txn.Exec(
			"DELETE FROM containers WHERE instance=?;",
			meta.Id,
		); err != nil {
			return err
		}
		active := make(map[string]struct{}, len(meta.Containers))
		for name, port := range meta.Ports {
			if _, err := txn.Exec(
				"INSERT INTO portAssignments(instance, name, port) VALUES (?, ?, ?);",
				meta.Id,
				name,
				port,
			); err != nil {
				return err
			}
		}
		for _, containerID := range meta.Containers {
			active[containerID] = struct{}{}
			if _, err := txn.Exec(
				"DELETE FROM retiredContainers WHERE id=?;",
				containerID,
			); err != nil {
				return err
			}
			if _, err := txn.Exec(
				"INSERT INTO containers(instance, id) VALUES (?, ?);",
				meta.Id,
				containerID,
			); err != nil {
				return err
			}
		}
		for _, containerID := range previous {
			if _, stillActive := active[containerID]; stillActive {
				continue
			}
			if _, err := txn.Exec(
				"INSERT OR IGNORE INTO retiredContainers(id) VALUES (?);",
				containerID,
			); err != nil {
				return err
			}
		}
		return nil
	})
}

func (m *Manager) lookupInstanceMetadata(instance InstanceId) (*InstanceMetadata, error) {
	metadata := new(InstanceMetadata)
	txn, err := m.db.Beginx()
	if err != nil {
		return metadata, fmt.Errorf("could not begin instance lookup: %w", err)
	}

	err = txn.Get(metadata, "SELECT * FROM instances WHERE id=?", instance)
	if isEmptyQueryError(err) {
		err = unknownInstanceIdError(instance)
	}

	ports := []struct {
		Name string
		Port int
	}{}
	if err == nil {
		err = txn.Select(&ports, "SELECT name, port FROM portAssignments WHERE instance=?", instance)
	}

	metadata.Ports = make(map[string]int)
	for _, kvPair := range ports {
		metadata.Ports[kvPair.Name] = kvPair.Port
	}

	metadata.Containers = []string{}
	if err == nil {
		err = txn.Select(&metadata.Containers, "SELECT id FROM containers WHERE instance=?", instance)
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

func (m *Manager) removeContainerMetadata(
	instance InstanceId,
	containerID string,
) error {
	result, err := m.db.Exec(
		"DELETE FROM containers WHERE instance=? AND id=?;",
		instance,
		containerID,
	)
	if err != nil {
		return err
	}
	_, err = result.RowsAffected()
	return err
}

func (m *Manager) removeInstancePorts(instance InstanceId) error {
	_, err := m.db.Exec(
		"DELETE FROM portAssignments WHERE instance=?;",
		instance,
	)
	return err
}

func (m *Manager) retiredContainerIDs() ([]string, error) {
	var ids []string
	err := m.db.Select(&ids, "SELECT id FROM retiredContainers ORDER BY id;")
	return ids, err
}

func (m *Manager) retireContainer(containerID string) error {
	_, err := m.db.Exec(
		"INSERT OR IGNORE INTO retiredContainers(id) VALUES (?);",
		containerID,
	)
	return err
}

func (m *Manager) forgetRetiredContainer(containerID string) error {
	_, err := m.db.Exec(
		"DELETE FROM retiredContainers WHERE id=?;",
		containerID,
	)
	return err
}

func (m *Manager) retireNetwork(name string) error {
	_, err := m.db.Exec(
		"INSERT OR IGNORE INTO retiredNetworks(name) VALUES (?);",
		name,
	)
	return err
}

func (m *Manager) retiredNetworkNames() ([]string, error) {
	var names []string
	err := m.db.Select(&names, "SELECT name FROM retiredNetworks ORDER BY name;")
	return names, err
}

func (m *Manager) forgetRetiredNetwork(name string) error {
	_, err := m.db.Exec("DELETE FROM retiredNetworks WHERE name=?;", name)
	return err
}

func (m *Manager) removeInstanceMetadata(instance InstanceId) error {
	_, err := m.db.Exec("DELETE FROM instances WHERE id=?", instance)
	return err
}

const removedSchemaInstancesQuery = `
	SELECT instances.id
	FROM instances
	JOIN builds ON instances.build = builds.id
	WHERE builds.schema = ? AND instancecount = ?;`

func (m *Manager) removedSchemaInstances(schema string) ([]InstanceId, error) {
	instances := []InstanceId{}
	err := m.db.Select(&instances, removedSchemaInstancesQuery, schema, LOCKED)
	return instances, err
}

const buildInstancesQuery = `
	SELECT instances.id
	FROM instances
	WHERE build = ?;`

func (m *Manager) getBuildInstances(build BuildId) ([]InstanceId, error) {
	instances := []InstanceId{}
	err := m.db.Select(&instances, buildInstancesQuery, build)
	return instances, err
}

func (m *Manager) incompleteInstanceIDs() ([]InstanceId, error) {
	instances := []InstanceId{}
	err := m.db.Select(
		&instances,
		`SELECT instances.id
		 FROM instances
		 LEFT JOIN containers ON containers.instance = instances.id
		 GROUP BY instances.id
		 HAVING COUNT(containers.id) = 0
		 ORDER BY instances.id;`,
	)
	return instances, err
}

func (m *Manager) trackedContainerIDs() ([]string, error) {
	containers := []string{}
	err := m.db.Select(
		&containers,
		"SELECT id FROM containers ORDER BY id;",
	)
	return containers, err
}

const recordInstanceSolveQuery = `
	UPDATE instances
	SET lastsolved = :lastsolved
	WHERE id = :id AND lastsolved < :lastsolved;`

const recordBuildSolveQuery = `
	UPDATE builds
	SET lastsolved = :lastsolved
	WHERE id = :build AND lastsolved < :lastsolved;`

func (m *Manager) recordSolve(instance *InstanceMetadata) error {
	return withTransaction(m.db, func(txn *sqlx.Tx) error {
		if _, err := txn.NamedExec(recordInstanceSolveQuery, instance); err != nil {
			return fmt.Errorf("could not record instance solve: %w", err)
		}
		if _, err := txn.NamedExec(recordBuildSolveQuery, instance); err != nil {
			return fmt.Errorf("could not record build solve: %w", err)
		}
		return nil
	})
}
