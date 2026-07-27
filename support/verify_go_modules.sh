#!/usr/bin/env bash

set -euo pipefail

go mod download
go mod verify
go mod tidy -diff

verify_dependency_pair() {
    local parent_module="$1"
    local child_module="$2"
    local pair_name="$3"
    local parent_version
    local selected_child_version
    local required_child_version=""

    parent_version="$(go list -m -f '{{.Version}}' "${parent_module}")"
    selected_child_version="$(go list -m -f '{{.Version}}' "${child_module}")"

    while read -r source dependency; do
        if [[ "${source}" == "${parent_module}@${parent_version}" ]] &&
            [[ "${dependency}" == "${child_module}@"* ]]; then
            required_child_version="${dependency#"${child_module}@"}"
            break
        fi
    done < <(go mod graph)

    if [[ -z "${required_child_version}" ]]; then
        echo "could not determine the ${child_module} version required by ${parent_module}@${parent_version}" >&2
        return 1
    fi

    if [[ "${selected_child_version}" != "${required_child_version}" ]]; then
        echo "${pair_name} dependency versions must be updated together" >&2
        echo "  ${parent_module}@${parent_version} requires ${child_module}@${required_child_version}" >&2
        echo "  go.mod selects ${child_module}@${selected_child_version}" >&2
        return 1
    fi

    echo "${pair_name} dependency pair verified: ${parent_module}@${parent_version} -> ${child_module}@${selected_child_version}"
}

# The split Moby client and API use separate version schemes but are released
# as a tested pair. Do not allow minimal version selection or an independent
# direct bump to silently combine versions that upstream did not pair.
verify_dependency_pair \
    "github.com/moby/moby/client" \
    "github.com/moby/moby/api" \
    "Moby client/API"

# modernc.org/sqlite is generated against a specific modernc.org/libc release.
# Upstream explicitly describes this dependency as fragile and requires clients
# to select that exact libc version.
verify_dependency_pair \
    "modernc.org/sqlite" \
    "modernc.org/libc" \
    "modernc SQLite/libc"
