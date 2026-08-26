#!/usr/bin/env -S uv run --frozen --no-default-groups -- python
"""Prepare vendor-specific images for the Go-owned containerlab bootstrap."""

from __future__ import annotations

import argparse
import json
import logging
import os
import shutil

# All subprocess calls use fixed argv and an allowlisted executable resolved to
# an absolute path. Shell execution is never enabled.
import subprocess  # nosec B404
import tarfile
import tempfile
from pathlib import Path
from typing import TYPE_CHECKING, Any, override
from urllib.error import URLError
from urllib.parse import urlsplit
from urllib.request import HTTPRedirectHandler, Request, build_opener

if TYPE_CHECKING:
    from http.client import HTTPMessage
    from typing import IO

VYOS_GITHUB_API = "https://api.github.com/repos/vyos/vyos-rolling-nightly-builds/releases"
VYOS_DOWNLOAD_BASE = "https://github.com/vyos/vyos-rolling-nightly-builds/releases/download"
_ALLOWED_HTTPS_HOSTS = frozenset({"api.github.com", "github.com"})
_ALLOWED_EXECUTABLES = frozenset(
    {
        "7z",
        "podman",
        "tar",
        "unsquashfs",
        "xorriso",
    },
)
_log = logging.getLogger("vendor-images")


class _VendorSecurityError(ValueError):
    """Report a rejected URL or executable boundary."""


class _ExecutableNotFoundError(FileNotFoundError):
    """Report an unavailable executable after allowlist validation."""


def _validated_https_url(url: str) -> str:
    """Return *url* only when it uses the exact outbound HTTPS allowlist."""
    parsed = urlsplit(url)
    try:
        port = parsed.port
    except ValueError as exc:
        message = f"invalid outbound URL port: {url!r}"
        raise _VendorSecurityError(message) from exc
    if (
        parsed.scheme != "https"
        or parsed.hostname not in _ALLOWED_HTTPS_HOSTS
        or parsed.username is not None
        or parsed.password is not None
        or port not in (None, 443)
    ):
        message = f"outbound URL is not allowlisted: {url!r}"
        raise _VendorSecurityError(message)
    return url


class _AllowlistedRedirectHandler(HTTPRedirectHandler):
    """Reject redirects outside the exact outbound HTTPS allowlist."""

    @override
    def redirect_request(
        self,
        req: Request,
        fp: IO[bytes],
        code: int,
        msg: str,
        headers: HTTPMessage,
        newurl: str,
    ) -> Request | None:
        """Validate a redirect target before constructing its request."""
        _validated_https_url(newurl)
        return super().redirect_request(req, fp, code, msg, headers, newurl)


def _resolve_executable(command: str) -> str:
    """Resolve an allowlisted command to an absolute executable path."""
    if command not in _ALLOWED_EXECUTABLES:
        message = f"executable is not allowlisted: {command!r}"
        raise _VendorSecurityError(message)
    found = shutil.which(command)
    if found is None:
        message = f"allowlisted executable not found: {command}"
        raise _ExecutableNotFoundError(message)
    resolved = Path(found)
    if not resolved.is_file() or not os.access(resolved, os.X_OK):
        message = f"allowlisted executable is not executable: {resolved}"
        raise _ExecutableNotFoundError(message)
    return str(resolved.absolute())


def _run(
    command: list[str],
    *,
    check: bool = True,
    capture: bool = False,
    dry_run: bool = False,
) -> subprocess.CompletedProcess[str]:
    """Execute one fixed-argv allowlisted command."""
    if not command:
        message = "command must not be empty"
        raise _VendorSecurityError(message)
    if dry_run:
        if command[0] not in _ALLOWED_EXECUTABLES:
            message = f"executable is not allowlisted: {command[0]!r}"
            raise _VendorSecurityError(message)
        _log.info("[dry-run] %s", " ".join(command))
        return subprocess.CompletedProcess(command, 0, stdout="", stderr="")
    resolved = [_resolve_executable(command[0]), *command[1:]]
    return subprocess.run(  # nosec B603  # noqa: S603
        resolved,
        check=check,
        text=True,
        capture_output=capture,
    )


def _image_exists(reference: str, *, dry_run: bool = False) -> bool:
    """Return whether an image reference exists in local Podman storage."""
    if dry_run:
        return False
    result = _run(
        ["podman", "image", "exists", reference],
        check=False,
        capture=True,
    )
    return result.returncode == 0


def _which(name: str) -> str | None:
    """Locate an executable on PATH."""
    return shutil.which(name)


def _latest_vyos_version() -> str:
    """Fetch the latest VyOS rolling release tag from GitHub API."""
    request = Request(  # noqa: S310
        _validated_https_url(f"{VYOS_GITHUB_API}/latest"),
        headers={
            "Accept": "application/vnd.github+json",
            "User-Agent": "gobfd-bootstrap",
        },
    )
    try:
        opener = build_opener(_AllowlistedRedirectHandler())
        with opener.open(request, timeout=30) as response:
            data: dict[str, Any] = json.loads(response.read())
    except (URLError, json.JSONDecodeError) as exc:
        message = f"query VyOS releases: {exc}"
        raise RuntimeError(message) from exc
    tag = data.get("tag_name", "")
    if not isinstance(tag, str) or not tag:
        message = "query VyOS releases: response lacks tag_name"
        raise RuntimeError(message)
    return tag


def _download_vyos_iso(version: str, destination: Path) -> Path:
    """Download one VyOS rolling ISO."""
    filename = f"vyos-{version}-generic-amd64.iso"
    url = f"{VYOS_DOWNLOAD_BASE}/{version}/{filename}"
    target = destination / filename
    request = Request(  # noqa: S310
        _validated_https_url(url),
        headers={"User-Agent": "gobfd-bootstrap"},
    )
    try:
        opener = build_opener(_AllowlistedRedirectHandler())
        with opener.open(request, timeout=600) as response, target.open("wb") as output:
            while chunk := response.read(1024 * 1024):
                output.write(chunk)
    except (OSError, URLError) as exc:
        message = f"download VyOS ISO: {exc}"
        raise RuntimeError(message) from exc
    if target.stat().st_size == 0:
        message = f"download VyOS ISO: empty response for {url}"
        raise RuntimeError(message)
    return target


def _extract_squashfs_from_iso(iso_path: Path, work_dir: Path) -> Path:
    """Extract filesystem.squashfs from a VyOS ISO."""
    squashfs_path = work_dir / "filesystem.squashfs"
    if _which("7z"):
        _run(
            ["7z", "x", "-y", f"-o{work_dir}", str(iso_path), "live/filesystem.squashfs"],
            capture=True,
        )
        extracted = work_dir / "live" / "filesystem.squashfs"
        if extracted.exists():
            extracted.rename(squashfs_path)
            return squashfs_path
    if _which("xorriso"):
        _run(
            [
                "xorriso",
                "-osirrox",
                "on",
                "-indev",
                str(iso_path),
                "-extract",
                "/live/filesystem.squashfs",
                str(squashfs_path),
            ],
            capture=True,
        )
        if squashfs_path.exists():
            return squashfs_path
    message = "extract VyOS ISO: install 7z or xorriso"
    raise RuntimeError(message)


def _extract_squashfs_root(squashfs_path: Path, work_dir: Path) -> Path:
    """Extract squashfs into a rootfs directory."""
    if not _which("unsquashfs"):
        message = "extract VyOS rootfs: unsquashfs not found"
        raise RuntimeError(message)
    rootfs = work_dir / "rootfs"
    _run(["unsquashfs", "-f", "-d", str(rootfs), str(squashfs_path)], capture=True)
    return rootfs


def _import_vyos_rootfs(rootfs: Path) -> None:
    """Tar a rootfs and pipe it into podman import."""
    with (
        subprocess.Popen(  # nosec B603  # noqa: S603
            [_resolve_executable("tar"), "-C", str(rootfs), "-c", "."],
            stdout=subprocess.PIPE,
        ) as tar_process,
        subprocess.Popen(  # nosec B603  # noqa: S603
            [_resolve_executable("podman"), "import", "-", "vyos:latest"],
            stdin=tar_process.stdout,
        ) as import_process,
    ):
        if tar_process.stdout:
            tar_process.stdout.close()
        import_status = import_process.wait()
        tar_status = tar_process.wait()
    if tar_status != 0 or import_status != 0:
        message = f"import VyOS rootfs: tar={tar_status} podman={import_status}"
        raise RuntimeError(message)


def _prepare_vyos(arguments: argparse.Namespace) -> bool:
    """Prepare the vyos:latest image expected by the topology."""
    if arguments.skip_pull and _image_exists("vyos:latest"):
        return True
    if _image_exists("vyos:latest", dry_run=arguments.dry_run) and not arguments.dry_run:
        return True
    if not arguments.iso and _image_exists(arguments.image, dry_run=arguments.dry_run):
        _run(["podman", "tag", arguments.image, "vyos:latest"], dry_run=arguments.dry_run)
        return True
    if arguments.dry_run:
        _log.info("[dry-run] would prepare vyos:latest from %s", arguments.image)
        return True
    if not _which("unsquashfs") or not (_which("7z") or _which("xorriso")):
        _log.error("VyOS ISO extraction requires unsquashfs and either 7z or xorriso")
        return False
    with tempfile.TemporaryDirectory(prefix="vyos-bootstrap-") as temporary:
        work = Path(temporary)
        if arguments.iso:
            iso_path = Path(arguments.iso)
            if not iso_path.is_file():
                _log.error("VyOS ISO not found: %s", iso_path)
                return False
        else:
            version = arguments.version
            if version == "latest":
                version = _latest_vyos_version()
            iso_path = _download_vyos_iso(version, work)
        squashfs = _extract_squashfs_from_iso(iso_path, work)
        rootfs = _extract_squashfs_root(squashfs, work)
        _import_vyos_rootfs(rootfs)
    result = _run(
        [
            "podman",
            "run",
            "--rm",
            "vyos:latest",
            "/bin/bash",
            "-c",
            "cat /etc/vyos-release 2>/dev/null || echo 'VyOS image imported'",
        ],
        check=False,
        capture=True,
    )
    return result.returncode == 0


def _parse_loaded_image(stdout: str) -> str:
    """Parse an image reference from podman load output."""
    for line in stdout.splitlines():
        if "Loaded image" not in line:
            continue
        if "image(s):" in line:
            return line.split("image(s):", 1)[-1].strip()
        if "image:" in line:
            return line.split("image:", 1)[-1].strip()
    return ""


def _import_arista(arguments: argparse.Namespace) -> bool:
    """Import an operator-supplied Arista cEOS archive."""
    path = Path(arguments.archive)
    if not path.is_file():
        _log.error("Arista archive not found: %s", path)
        return False
    if _image_exists(arguments.tag, dry_run=arguments.dry_run):
        return True
    result = _run(
        ["podman", "load", "-i", str(path)],
        capture=True,
        check=False,
        dry_run=arguments.dry_run,
    )
    if result.returncode != 0:
        _log.error("podman load failed for Arista: %s", result.stderr.strip())
        return False
    loaded = _parse_loaded_image(result.stdout or "")
    if loaded and loaded != arguments.tag:
        tag_result = _run(
            ["podman", "tag", loaded, arguments.tag],
            check=False,
            dry_run=arguments.dry_run,
        )
        if tag_result.returncode != 0:
            return False
    return True


def _import_cisco_nested(path: Path) -> bool:
    """Extract and load a nested Docker archive from an XRd tarball."""
    with tempfile.TemporaryDirectory(prefix="xrd-bootstrap-") as temporary:
        work = Path(temporary)
        try:
            with tarfile.open(path) as archive:
                members = [
                    member
                    for member in archive.getmembers()
                    if member.name.endswith((".tar", ".tar.gz", ".tgz"))
                ]
                if not members:
                    return False
                archive.extract(members[0], work, filter="data")
                inner_path = work / members[0].name
        except (OSError, tarfile.TarError) as exc:
            _log.error("extract Cisco XRd archive: %s", exc)
            return False
        result = _run(
            ["podman", "load", "-i", str(inner_path)],
            capture=True,
            check=False,
        )
        return result.returncode == 0


def _import_cisco(arguments: argparse.Namespace) -> bool:
    """Import an operator-supplied Cisco XRd archive."""
    path = Path(arguments.archive)
    if not path.is_file():
        _log.error("Cisco XRd archive not found: %s", path)
        return False
    if _image_exists(arguments.tag, dry_run=arguments.dry_run):
        return True
    if arguments.dry_run:
        _log.info("[dry-run] would import Cisco XRd as %s", arguments.tag)
        return True
    result = _run(["podman", "load", "-i", str(path)], capture=True, check=False)
    if result.returncode == 0:
        return True
    return _import_cisco_nested(path)


def _parse_args() -> argparse.Namespace:
    """Parse the narrow vendor-image command line."""
    parser = argparse.ArgumentParser(description="Prepare containerlab vendor images.")
    parser.add_argument("-v", "--verbose", action="store_true")
    subcommands = parser.add_subparsers(dest="operation", required=True)

    vyos = subcommands.add_parser("vyos")
    vyos.add_argument("--version", default="latest")
    vyos.add_argument("--image", required=True)
    vyos.add_argument("--iso")
    vyos.add_argument("--skip-pull", action="store_true")
    vyos.add_argument("--dry-run", action="store_true")

    for name in ("arista", "cisco"):
        commercial = subcommands.add_parser(name)
        commercial.add_argument("--archive", required=True)
        commercial.add_argument("--tag", required=True)
        commercial.add_argument("--dry-run", action="store_true")
    return parser.parse_args()


def main() -> int:
    """Prepare exactly one requested vendor image."""
    arguments = _parse_args()
    logging.basicConfig(
        level=logging.DEBUG if arguments.verbose else logging.INFO,
        format="%(levelname)s %(message)s",
    )
    try:
        if arguments.operation == "vyos":
            success = _prepare_vyos(arguments)
        elif arguments.operation == "arista":
            success = _import_arista(arguments)
        else:
            success = _import_cisco(arguments)
    except (OSError, RuntimeError, subprocess.SubprocessError) as exc:
        _log.error("%s", exc)
        return 1
    return 0 if success else 1


if __name__ == "__main__":
    raise SystemExit(main())
