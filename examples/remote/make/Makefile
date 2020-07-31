# This Makefile is intended to be fully flexible for most simple binary
# exploitation challenges.  In general, changing the "PROG" and "CFLAGS"
# variables is likely sufficient for many cases.

PROG = BinEx101
FLAG ?= flag{dummy_value} # Only set if not defined in the environment
FLAG_FILE ?= flag.txt

CFLAGS = -O0 -g -D'FLAG_FILE="$(FLAG_FILE)"'
CXXFLAGS = -O0 -g -D'FLAG_FILE="$(FLAG_FILE)"'

# Having this be the first "target" makes the developer's life easier because
# it allows `make` on the command line to act as short-hand for building all of
# the necessary components for cmgr.
.PHONY: all
all: main artifacts.tar.gz metadata.json

# Required targets:
#   Each of the four targets below are required by cmgr in order for a remote
# "make" challenge to function correctly.  Notice that the "main" and "run"
# end-points do not need to create a file by that name.
.PHONY: main
main: $(PROG) $(FLAG_FILE)

.PHONY: run
run: main
	@./$(PROG)

artifacts.tar.gz: $(PROG) $(PROG).c
	tar czvf $@ $^

metadata.json:
	@echo "Creating the metadata file..."
	@echo "{\"flag\":\"$(FLAG)\"}" > metadata.json

# Additional targets:
#   Here we define targets for any files we need that "make" does not know how
# to correctly generate.  In this specific case, that is only the flag file.
# In particular, notice that we do not have to list how to build our program
# or the corresponding object files because make's implicit rules are generally
# good enough for single file programs.
$(FLAG_FILE):
	@echo "Creating the flag file..."
	@echo -n $(FLAG) > $(FLAG_FILE)

clean:
	rm $(PROG) $(FLAG_FILE)
