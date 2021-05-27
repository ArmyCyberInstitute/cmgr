package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"io/ioutil"
	"path/filepath"

	"gopkg.in/yaml.v2"

	"github.com/ArmyCyberInstitute/cmgr/cmgr"
)

func listSchemas(mgr *cmgr.Manager, args []string) int {
	parser := flag.NewFlagSet("list-schemas", flag.ExitOnError)
	updateUsage(parser, "")
	parser.Parse(args)

	if parser.NArg() != 0 {
		parser.Usage()
		return USAGE_ERROR
	}

	schemaList, err := mgr.ListSchemas()
	if err != nil {
		fmt.Printf("error: %s\n", err)
		return RUNTIME_ERROR
	}

	for _, schema := range schemaList {
		fmt.Println(schema)
	}

	return NO_ERROR
}

func addSchema(mgr *cmgr.Manager, args []string) int {
	parser := flag.NewFlagSet("add-schema", flag.ExitOnError)
	updateUsage(parser, "<schema file>")
	parser.Parse(args)

	if parser.NArg() != 1 {
		parser.Usage()
		return USAGE_ERROR
	}

	schema, retCode := loadSchema(parser.Arg(0))
	if retCode != NO_ERROR {
		return retCode
	}

	errs := mgr.CreateSchema(schema)

	for _, err := range errs {
		retCode = RUNTIME_ERROR
		fmt.Printf("error: %s\n", err)
	}

	return retCode
}

func updateSchema(mgr *cmgr.Manager, args []string) int {
	parser := flag.NewFlagSet("update-schema", flag.ExitOnError)
	updateUsage(parser, "<schema file>")
	parser.Parse(args)

	if parser.NArg() != 1 {
		parser.Usage()
		return USAGE_ERROR
	}

	schema, retCode := loadSchema(parser.Arg(0))
	if retCode != NO_ERROR {
		return retCode
	}

	errs := mgr.UpdateSchema(schema)

	for _, err := range errs {
		retCode = RUNTIME_ERROR
		fmt.Printf("error: %s\n", err)
	}

	return retCode
}

func removeSchema(mgr *cmgr.Manager, args []string) int {
	parser := flag.NewFlagSet("remove-schema", flag.ExitOnError)
	updateUsage(parser, "<schema name>")
	parser.Parse(args)

	if parser.NArg() != 1 {
		parser.Usage()
		return USAGE_ERROR
	}

	err := mgr.DeleteSchema(parser.Arg(0))
	if err != nil {
		fmt.Printf("error: %s\n", err)
		return RUNTIME_ERROR
	}

	return NO_ERROR
}

func showSchema(mgr *cmgr.Manager, args []string) int {
	parser := flag.NewFlagSet("show-schema", flag.ExitOnError)
	updateUsage(parser, "<schema name>")
	parser.Parse(args)

	if parser.NArg() != 1 {
		parser.Usage()
		return USAGE_ERROR
	}

	state, err := mgr.GetSchemaState(parser.Arg(0))
	if err != nil {
		fmt.Printf("error: %s\n", err)
		return RUNTIME_ERROR
	}

	data, err := json.MarshalIndent(state, "", "    ")
	if err != nil {
		fmt.Printf("error: %s\n", err)
		return RUNTIME_ERROR
	}

	fmt.Println(string(data))
	return NO_ERROR
}

func loadSchema(fname string) (*cmgr.Schema, int) {
	data, err := ioutil.ReadFile(fname)
	if err != nil {
		fmt.Printf("error: %s\n", err)
		return nil, RUNTIME_ERROR
	}

	var schema *cmgr.Schema
	retCode := NO_ERROR
	switch filepath.Ext(fname) {
	case ".json":
		err = json.Unmarshal(data, &schema)
	case ".yaml":
		err = yaml.Unmarshal(data, &schema)
	default:
		fmt.Printf("error: unrecognized file extension of '%s'; expected 'yaml' or 'json'\n", filepath.Ext(fname))
		retCode = USAGE_ERROR
	}

	if err != nil {
		fmt.Printf("error: %s\n", err)
		retCode = RUNTIME_ERROR
	}

	return schema, retCode
}
