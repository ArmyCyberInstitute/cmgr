// cmgr-oci-interceptor is an OCI runtime wrapper inspired by and adapted from
// picoCTF/oci-interceptor v0.2.2:
// https://github.com/picoCTF/oci-interceptor
//
// The original project is available under the Apache License 2.0. This
// modified implementation is distributed under cmgr's Apache License 2.0.
package main

import (
	"os"

	"github.com/ArmyCyberInstitute/cmgr/internal/ociinterceptor"
)

func main() {
	if len(os.Args) > 1 && os.Args[1] == ociinterceptor.RegisterSubcommand {
		os.Exit(
			ociinterceptor.RunRegisterCommand(
				os.Args[2:],
				os.Args[0],
				os.Stdout,
				os.Stderr,
			),
		)
	}

	os.Exit(
		ociinterceptor.RunRuntime(
			os.Args[1:],
			os.Stdin,
			os.Stdout,
			os.Stderr,
		),
	)
}
