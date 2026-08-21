#!/usr/bin/env sh
set -eu

target_goos="${GOPLS_GOOS:-linux}"
target_goarch="${GOPLS_GOARCH:-amd64}"
default_tag_profiles='integration,testcontainers,interop,interop_bgp,interop_rfc,interop_clab,e2e_core,e2e_overlay,e2e_linux,e2e_vendor
e2e_core_testcontainers'
tag_profiles="${GOPLS_TAGS:-${default_tag_profiles}}"
base_goflags="${GOFLAGS:-}"

export GOOS="${target_goos}"
export GOARCH="${target_goarch}"

profile_count=0
total_package_count=0
total_input_count=0
for target_tags in ${tag_profiles}; do
	profile_count=$((profile_count + 1))
	export GOFLAGS="${base_goflags}${base_goflags:+ }-tags=${target_tags}"

	packages="$(go list -f '{{.ImportPath}}' ./...)"
	package_count="$(printf '%s\n' "${packages}" | sed '/^$/d' | wc -l | tr -d '[:space:]')"
	if [ "${package_count}" -eq 0 ]; then
		printf 'gopls-check: no packages discovered; tags=%s\n' "${target_tags}" >&2
		exit 1
	fi

	inputs="$(
		printf '%s\n' "${packages}" | while IFS= read -r pkg; do
			go list -f '{{range .GoFiles}}{{printf "%s/%s\n" $.Dir .}}{{end}}{{range .TestGoFiles}}{{printf "%s/%s\n" $.Dir .}}{{end}}{{range .XTestGoFiles}}{{printf "%s/%s\n" $.Dir .}}{{end}}' "${pkg}"
		done
	)"
	input_count="$(printf '%s\n' "${inputs}" | sed '/^$/d' | wc -l | tr -d '[:space:]')"
	if [ "${input_count}" -eq 0 ]; then
		printf 'gopls-check: no Go inputs discovered; tags=%s\n' "${target_tags}" >&2
		exit 1
	fi

	output="$(
		printf '%s\n' "${packages}" | while IFS= read -r pkg; do
			files="$(
				go list -f '{{range .GoFiles}}{{printf "%s/%s\n" $.Dir .}}{{end}}{{range .TestGoFiles}}{{printf "%s/%s\n" $.Dir .}}{{end}}{{range .XTestGoFiles}}{{printf "%s/%s\n" $.Dir .}}{{end}}' "${pkg}"
			)"
			if [ -n "${files}" ]; then
				# Check one package at a time so gopls does not mix unrelated
				# GOOS-specific package scopes when Linux-only files are present.
				# shellcheck disable=SC2086
				gopls check ${files}
			fi
		done 2>&1
	)"

	if [ -n "${output}" ]; then
		printf '%s\n' "${output}"
		exit 1
	fi

	total_package_count=$((total_package_count + package_count))
	total_input_count=$((total_input_count + input_count))
done

printf 'gopls-check: no diagnostics across %s tag profiles, %s package checks, and %s Go input checks; GOOS=%s GOARCH=%s\n' \
	"${profile_count}" "${total_package_count}" "${total_input_count}" "${target_goos}" "${target_goarch}"
