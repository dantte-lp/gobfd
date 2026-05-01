#!/usr/bin/env sh
set -eu

target_goos="${GOPLS_GOOS:-linux}"
target_goarch="${GOPLS_GOARCH:-amd64}"
target_tags="${GOPLS_TAGS:-integration,interop,interop_bgp,interop_rfc,interop_clab,e2e_core,e2e_overlay,e2e_linux,e2e_vendor}"

export GOOS="${target_goos}"
export GOARCH="${target_goarch}"
export GOFLAGS="${GOFLAGS:-} -tags=${target_tags}"

output="$(
	go list -f '{{.ImportPath}}' ./... | while IFS= read -r pkg; do
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

printf 'gopls-check: no diagnostics for GOOS=%s GOARCH=%s tags=%s\n' \
	"${target_goos}" "${target_goarch}" "${target_tags}"
