package cmgr

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
	txn := m.db.MustBegin()
	var err error
	for name, port := range meta.Ports {
		_, err = txn.Exec("INSERT INTO portAssignments(instance, name, port) VALUES (?, ?, ?);",
			meta.Id,
			name,
			port)

		if err != nil {
			m.log.errorf("failed to record port assignment: %s", err)
			cerr := txn.Rollback()
			if cerr != nil { // If rollback fails, we're in trouble.
				m.log.error(cerr)
				err = cerr
			}
			return err
		}
	}

	for _, containerId := range meta.Containers {
		_, err = txn.Exec("INSERT INTO containers(instance, id) VALUES (?, ?);",
			meta.Id,
			containerId)

		if err != nil {
			m.log.errorf("failed to record containers: %s", err)
			cerr := txn.Rollback()
			if cerr != nil { // If rollback fails, we're in trouble.
				m.log.error(cerr)
				err = cerr
			}
			return err
		}
	}

	err = txn.Commit()
	if err != nil { // It's undocumented what this means...
		m.log.error(err)
	}
	return err
}

func (m *Manager) lookupInstanceMetadata(instance InstanceId) (*InstanceMetadata, error) {
	metadata := new(InstanceMetadata)
	txn := m.db.MustBegin()

	err := txn.Get(metadata, "SELECT * FROM instances WHERE id=?", instance)
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
			m.log.errorf("rollback failed: %s", err)
			err = closeErr
		}
	}

	return metadata, err
}

func (m *Manager) removeContainersMetadata(instance *InstanceMetadata) error {
	txn := m.db.MustBegin()
	_, err := txn.Exec("DELETE FROM portAssignments WHERE instance=?;", instance.Id)
	if err == nil {
		_, err = txn.Exec("DELETE FROM containers WHERE instance=?", instance.Id)
	}

	if err == nil {
		err = txn.Commit()
		if err != nil {
			m.log.errorf("failed to commit deletion of container metadata: %s", err)
		}
	} else {
		m.log.errorf("failed to delete container metadata: %s", err)
		closeErr := txn.Rollback()
		if closeErr != nil {
			m.log.errorf("rollback failed: %s", err)
			err = closeErr
		}
	}

	instance.Containers = []string{}
	instance.Ports = make(map[string]int)

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

const recordInstanceSolveQuery = `
	UPDATE instances
	SET lastsolved = :lastsolved
	WHERE id = :id AND lastsolved < :lastsolved;`

const recordBuildSolveQuery = `
	UPDATE builds
	SET lastsolved = :lastsolved
	WHERE id = :build AND lastsolved < :lastsolved;`

func (m *Manager) recordSolve(instance *InstanceMetadata) error {
	txn := m.db.MustBegin()
	_, err := txn.NamedExec(recordInstanceSolveQuery, instance)
	if err == nil {
		_, err = txn.NamedExec(recordBuildSolveQuery, instance)
	}

	if err == nil {
		err = txn.Commit()
		if err != nil {
			m.log.errorf("failed to commit deletion of container metadata: %s", err)
		}
	} else {
		m.log.errorf("failed to delete container metadata: %s", err)
		closeErr := txn.Rollback()
		if closeErr != nil {
			m.log.errorf("rollback failed: %s", err)
			err = closeErr
		}
	}
	return err
}

func (m *Manager) lookupBuildInstances(build BuildId) ([]*InstanceMetadata, error) {
	var instances []*InstanceMetadata
	err := m.db.Select(&instances, "SELECT * FROM instances WHERE build = ?", build)
	if err != nil {
		return nil, err
	}

	if len(instances) == 0 {
		return instances, nil
	}

	// Fetch all ports for these instances
	ports := []struct {
		Instance InstanceId `db:"instance"`
		Name     string     `db:"name"`
		Port     int        `db:"port"`
	}{}
	err = m.db.Select(&ports, "SELECT instance, name, port FROM portAssignments WHERE instance IN (SELECT id FROM instances WHERE build = ?)", build)
	if err != nil {
		return nil, err
	}

	// Map ports to instances
	portMap := make(map[InstanceId]map[string]int)
	for _, p := range ports {
		if _, ok := portMap[p.Instance]; !ok {
			portMap[p.Instance] = make(map[string]int)
		}
		portMap[p.Instance][p.Name] = p.Port
	}

	// Fetch all containers for these instances
	containers := []struct {
		Instance InstanceId `db:"instance"`
		Id       string     `db:"id"`
	}{}
	err = m.db.Select(&containers, "SELECT instance, id FROM containers WHERE instance IN (SELECT id FROM instances WHERE build = ?)", build)
	if err != nil {
		return nil, err
	}

	// Map containers to instances
	containerMap := make(map[InstanceId][]string)
	for _, c := range containers {
		containerMap[c.Instance] = append(containerMap[c.Instance], c.Id)
	}

	// Combine
	for _, inst := range instances {
		inst.Ports = portMap[inst.Id]
		if inst.Ports == nil {
			inst.Ports = make(map[string]int)
		}
		inst.Containers = containerMap[inst.Id]
		if inst.Containers == nil {
			inst.Containers = []string{}
		}
	}

	return instances, nil
}
