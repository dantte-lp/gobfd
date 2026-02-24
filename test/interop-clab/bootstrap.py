#!/usr/bin/env python3
"""Bootstrap the GoBFD vendor interop lab from a clean machine.

Automates full image preparation: pulls open-source NOS images, builds VyOS
from ISO, imports commercial tarballs, builds GoBFD, then optionally delegates
to run.sh for deployment and testing.

Requires Python 3.12+, podman, go. No external Python dependencies.
"""

from __future__ import annotations

import argparse
import dataclasses
import json
import logging
import os
import shutil
import subprocess
import sys
import tarfile
import tempfile
from concurrent.futures import ThreadPoolExecutor, as_completed
from pathlib import Path
from typing import Any
from urllib.error import URLError
from urllib.request import Request, urlopen

# ---------------------------------------------------------------------------
# Constants
# ---------------------------------------------------------------------------

SCRIPT_DIR = Path(__file__).resolve().parent
PROJECT_ROOT = SCRIPT_DIR.parent.parent

MIN_PYTHON = (3, 12)
MIN_DISK_GB = 15

OPEN_SOURCE_IMAGES: dict[str, str] = {
    "nokia": "ghcr.io/nokia/srlinux:{nokia_tag}",
    "sonic": "docker.io/netreplica/docker-sonic-vs:{sonic_tag}",
    "frr": "quay.io/frrouting/frr:{frr_tag}",
    "gobgp": "docker.io/jauderho/gobgp:v3.33.0",
    "golang": "docker.io/golang:1.26-trixie",
    "alpine": "docker.io/library/alpine:3.21",
}

# Images expected by run.sh after bootstrap.
INVENTORY: list[dict[str, str]] = [
    {
        "label": "Nokia SR Linux",
        "ref_tpl": "ghcr.io/nokia/srlinux:{nokia_tag}",
        "source": "pull",
    },
    {
        "label": "SONiC-VS",
        "ref_tpl": "docker.io/netreplica/docker-sonic-vs:{sonic_tag}",
        "source": "pull",
    },
    {
        "label": "VyOS",
        "ref_tpl": "vyos:latest",
        "source": "built from ISO",
    },
    {
        "label": "FRRouting",
        "ref_tpl": "quay.io/frrouting/frr:{frr_tag}",
        "source": "pull",
    },
    {
        "label": "GoBFD",
        "ref_tpl": "gobfd-clab:latest",
        "source": "built from source",
    },
    {
        "label": "Arista cEOS",
        "ref_tpl": "ceos:{arista_tag}",
        "source": "--arista-image",
    },
    {
        "label": "Cisco XRd",
        "ref_tpl": "ios-xr/xrd-control-plane:{cisco_tag}",
        "source": "--cisco-image",
    },
]

VYOS_GITHUB_API = (
    "https://api.github.com/repos/vyos/vyos-rolling-nightly-builds/releases"
)
VYOS_DOWNLOAD_BASE = (
    "https://github.com/vyos/vyos-rolling-nightly-builds/releases/download"
)

_log = logging.getLogger("bootstrap")


# ---------------------------------------------------------------------------
# Terminal colours (mutable state in a dataclass, not bare globals)
# ---------------------------------------------------------------------------


@dataclasses.dataclass
class _Colours:
    green: str = ""
    red: str = ""
    yellow: str = ""
    cyan: str = ""
    bold: str = ""
    reset: str = ""


_c = _Colours()


def _init_colours() -> None:
    """Populate escape codes when stderr is a terminal."""
    if sys.stderr.isatty():
        _c.green = "\033[32m"
        _c.red = "\033[31m"
        _c.yellow = "\033[33m"
        _c.cyan = "\033[36m"
        _c.bold = "\033[1m"
        _c.reset = "\033[0m"


class _Formatter(logging.Formatter):
    """Compact coloured log formatter."""

    def format(self, record: logging.LogRecord) -> str:
        """Format a log record with colour codes."""
        levels = {
            logging.DEBUG: f"{_c.cyan}DEBUG{_c.reset}",
            logging.INFO: f"{_c.green} INFO{_c.reset}",
            logging.WARNING: f"{_c.yellow} WARN{_c.reset}",
            logging.ERROR: f"{_c.red}ERROR{_c.reset}",
        }
        lvl = levels.get(record.levelno, str(record.levelno))
        return f"{lvl}  {record.getMessage()}"


# ---------------------------------------------------------------------------
# Helpers
# ---------------------------------------------------------------------------


def _run(
    cmd: list[str],
    *,
    check: bool = True,
    capture: bool = False,
    dry_run: bool = False,
    cwd: str | None = None,
) -> subprocess.CompletedProcess[str]:
    """Execute a command, returning its CompletedProcess."""
    _log.debug("exec: %s", " ".join(cmd))
    if dry_run:
        _log.info("[dry-run] %s", " ".join(cmd))
        return subprocess.CompletedProcess(cmd, 0, stdout="", stderr="")
    return subprocess.run(
        cmd,
        check=check,
        text=True,
        capture_output=capture,
        cwd=cwd,
    )


def _image_exists(ref: str, *, dry_run: bool = False) -> bool:
    """Check whether a container image exists locally."""
    if dry_run:
        return False
    r = _run(
        ["podman", "image", "exists", ref],
        check=False,
        capture=True,
    )
    return r.returncode == 0


def _disk_free_gb(path: str = "/") -> float:
    """Return free disk space in GiB."""
    st = os.statvfs(path)
    return (st.f_bavail * st.f_frsize) / (1024**3)


def _which(name: str) -> str | None:
    """Locate a binary on PATH."""
    return shutil.which(name)


# ---------------------------------------------------------------------------
# Phase 1: Preflight
# ---------------------------------------------------------------------------


def _preflight(dry_run: bool) -> list[str]:
    """Run preflight checks. Returns list of warnings (non-fatal)."""
    warnings: list[str] = []

    if sys.version_info < MIN_PYTHON:
        sys.exit(
            f"Python >= {'.'.join(map(str, MIN_PYTHON))} required "
            f"(running {sys.version})",
        )
    _log.info("Python %s", sys.version.split()[0])

    if not _which("podman"):
        sys.exit("podman not found in PATH")
    r = _run(
        ["podman", "version", "--format", "{{.Client.Version}}"],
        capture=True,
        check=False,
        dry_run=dry_run,
    )
    _log.info("podman %s", r.stdout.strip() if r.stdout else "OK")

    if not dry_run:
        _check_podman_socket()

    if not _which("go"):
        warnings.append(
            "go not found — GoBFD image build will fail (use --skip-build to skip)",
        )
    else:
        r = _run(
            ["go", "version"],
            capture=True,
            check=False,
            dry_run=dry_run,
        )
        _log.info("go: %s", r.stdout.strip() if r.stdout else "OK")

    for tool in ("ip", "ethtool", "nsenter"):
        if not _which(tool):
            warnings.append(
                f"{tool} not found — required by run.sh for veth creation",
            )

    if not dry_run:
        free = _disk_free_gb("/")
        if free < MIN_DISK_GB:
            warnings.append(
                f"Low disk space: {free:.1f} GB free (recommend >= {MIN_DISK_GB} GB)",
            )
        else:
            _log.info("disk: %.1f GB free", free)

    for w in warnings:
        _log.warning(w)

    return warnings


def _check_podman_socket() -> None:
    """Exit if no Podman API socket is reachable."""
    sock = Path("/run/podman/podman.sock")
    if sock.is_socket():
        return
    runtime_dir = os.environ.get(
        "XDG_RUNTIME_DIR",
        f"/run/user/{os.getuid()}",
    )
    user_sock = Path(runtime_dir, "podman", "podman.sock")
    if user_sock.is_socket():
        return
    sys.exit(
        "Podman socket not active — start with: systemctl --user start podman.socket",
    )


# ---------------------------------------------------------------------------
# Phase 2: Pull open-source images
# ---------------------------------------------------------------------------


def _pull_one(
    name: str,
    ref: str,
    *,
    skip_pull: bool,
    dry_run: bool,
) -> tuple[str, bool]:
    """Pull a single image. Returns (name, success)."""
    try:
        if skip_pull and _image_exists(ref):
            _log.info("%-10s %s  (exists, skipped)", name, ref)
            return name, True
        if not skip_pull and _image_exists(ref, dry_run=dry_run):
            _log.info("%-10s %s  (already present)", name, ref)
            return name, True
        _log.info("%-10s pulling %s …", name, ref)
        _run(["podman", "pull", "--quiet", ref], dry_run=dry_run)
        _log.info("%-10s %s  OK", name, ref)
        return name, True
    except subprocess.CalledProcessError as exc:
        _log.error(
            "%-10s pull failed (rc=%d): %s",
            name,
            exc.returncode,
            ref,
        )
        return name, False


def _pull_images(
    images: dict[str, str],
    *,
    max_workers: int = 3,
    skip_pull: bool = False,
    dry_run: bool = False,
) -> dict[str, bool]:
    """Pull multiple images in parallel. Returns {name: success}."""
    results: dict[str, bool] = {}
    with ThreadPoolExecutor(max_workers=max_workers) as pool:
        futures = {
            pool.submit(
                _pull_one,
                name,
                ref,
                skip_pull=skip_pull,
                dry_run=dry_run,
            ): name
            for name, ref in images.items()
        }
        for fut in as_completed(futures):
            name, ok = fut.result()
            results[name] = ok
    return results


# ---------------------------------------------------------------------------
# Phase 3: Build VyOS from ISO
# ---------------------------------------------------------------------------


def _latest_vyos_version() -> str:
    """Fetch the latest VyOS rolling release tag from GitHub API."""
    _log.info("querying GitHub for latest VyOS rolling release …")
    req = Request(
        f"{VYOS_GITHUB_API}/latest",
        headers={
            "Accept": "application/vnd.github+json",
            "User-Agent": "gobfd-bootstrap",
        },
    )
    try:
        with urlopen(req, timeout=30) as resp:
            data: dict[str, Any] = json.loads(resp.read())
            tag: str = data.get("tag_name", "")
            if not tag:
                sys.exit(
                    "could not determine latest VyOS release from GitHub API",
                )
            _log.info("latest VyOS rolling release: %s", tag)
            return tag
    except (URLError, json.JSONDecodeError) as exc:
        sys.exit(f"failed to query VyOS releases: {exc}")


def _download_vyos_iso(version: str, dest: Path) -> Path:
    """Download a VyOS rolling ISO to *dest* directory."""
    filename = f"vyos-{version}-generic-amd64.iso"
    url = f"{VYOS_DOWNLOAD_BASE}/{version}/{filename}"
    target = dest / filename

    _log.info("downloading VyOS ISO: %s", url)
    req = Request(url, headers={"User-Agent": "gobfd-bootstrap"})
    try:
        with urlopen(req, timeout=600) as resp:
            total = int(resp.headers.get("Content-Length", 0))
            downloaded = 0
            with open(target, "wb") as f:
                while chunk := resp.read(1024 * 1024):
                    f.write(chunk)
                    downloaded += len(chunk)
                    if total:
                        pct = downloaded * 100 // total
                        _log.info(
                            "  %d / %d MiB (%d%%)",
                            downloaded >> 20,
                            total >> 20,
                            pct,
                        )
    except URLError as exc:
        sys.exit(f"failed to download VyOS ISO: {exc}")

    _log.info(
        "saved %s (%d MiB)",
        target,
        target.stat().st_size >> 20,
    )
    return target


def _extract_squashfs_from_iso(
    iso_path: Path,
    work_dir: Path,
) -> Path:
    """Extract filesystem.squashfs from a VyOS ISO."""
    squashfs_path = work_dir / "filesystem.squashfs"

    if _which("7z"):
        _log.info("extracting squashfs from ISO via 7z …")
        _run(
            [
                "7z",
                "x",
                "-y",
                f"-o{work_dir}",
                str(iso_path),
                "live/filesystem.squashfs",
            ],
            capture=True,
        )
        extracted = work_dir / "live" / "filesystem.squashfs"
        if extracted.exists():
            extracted.rename(squashfs_path)
            return squashfs_path

    if _which("xorriso"):
        _log.info("extracting squashfs from ISO via xorriso …")
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

    sys.exit(
        "cannot extract squashfs from ISO: install '7z' (p7zip-full) or 'xorriso'",
    )


def _extract_squashfs_root(
    squashfs_path: Path,
    work_dir: Path,
) -> Path:
    """Extract squashfs into a rootfs directory."""
    if not _which("unsquashfs"):
        sys.exit("unsquashfs not found — install squashfs-tools")
    rootfs = work_dir / "rootfs"
    _log.info("extracting squashfs to %s …", rootfs)
    _run(
        ["unsquashfs", "-f", "-d", str(rootfs), str(squashfs_path)],
        capture=True,
    )
    return rootfs


def _import_vyos_rootfs(rootfs: Path) -> bool:
    """Tar a rootfs and pipe into podman import. Returns success."""
    _log.info("importing rootfs as vyos:latest …")
    with (
        subprocess.Popen(
            ["tar", "-C", str(rootfs), "-c", "."],
            stdout=subprocess.PIPE,
        ) as tar_proc,
        subprocess.Popen(
            ["podman", "import", "-", "vyos:latest"],
            stdin=tar_proc.stdout,
        ) as import_proc,
    ):
        if tar_proc.stdout:
            tar_proc.stdout.close()
        import_proc.wait()
        tar_proc.wait()
        if import_proc.returncode != 0:
            _log.error(
                "podman import failed (rc=%d)",
                import_proc.returncode,
            )
            return False
    return True


def _build_vyos(
    *,
    vyos_iso: str | None,
    vyos_version: str,
    skip_pull: bool,
    dry_run: bool,
) -> bool:
    """Build VyOS container image from ISO. Returns success."""
    if skip_pull and _image_exists("vyos:latest"):
        _log.info("VyOS image already exists (skipped)")
        return True
    if _image_exists("vyos:latest", dry_run=dry_run) and not dry_run:
        _log.info("VyOS image already present")
        return True
    if dry_run:
        _log.info("[dry-run] would build vyos:latest from ISO")
        return True

    if not _which("unsquashfs"):
        _log.error("unsquashfs not found — install squashfs-tools")
        return False
    if not (_which("7z") or _which("xorriso")):
        _log.error("need 7z (p7zip-full) or xorriso to extract ISO")
        return False

    with tempfile.TemporaryDirectory(
        prefix="vyos-bootstrap-",
    ) as tmpdir:
        work = Path(tmpdir)
        iso_path = _obtain_vyos_iso(
            vyos_iso=vyos_iso,
            vyos_version=vyos_version,
            work=work,
        )
        if iso_path is None:
            return False

        squashfs = _extract_squashfs_from_iso(iso_path, work)
        rootfs = _extract_squashfs_root(squashfs, work)

        if not _import_vyos_rootfs(rootfs):
            return False

        _verify_vyos_image()

    _log.info("VyOS image built successfully")
    return True


def _obtain_vyos_iso(
    *,
    vyos_iso: str | None,
    vyos_version: str,
    work: Path,
) -> Path | None:
    """Get VyOS ISO from local path or download."""
    if vyos_iso:
        iso_path = Path(vyos_iso)
        if not iso_path.is_file():
            _log.error("VyOS ISO not found: %s", iso_path)
            return None
        _log.info("using local VyOS ISO: %s", iso_path)
        return iso_path
    version = vyos_version if vyos_version != "latest" else _latest_vyos_version()
    return _download_vyos_iso(version, work)


def _verify_vyos_image() -> None:
    """Run a quick smoke test on the imported VyOS image."""
    r = _run(
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
    _log.info("VyOS verification: %s", r.stdout.strip())


# ---------------------------------------------------------------------------
# Phase 4: Import commercial images
# ---------------------------------------------------------------------------


def _parse_loaded_image(stdout: str) -> str:
    """Parse image reference from ``podman load`` output."""
    for line in stdout.splitlines():
        if "Loaded image" not in line:
            continue
        if "image(s):" in line:
            return line.split("image(s):", 1)[-1].strip()
        if "image:" in line:
            return line.split("image:", 1)[-1].strip()
    return ""


def _import_arista(
    tarball: str,
    tag: str,
    *,
    dry_run: bool,
) -> bool:
    """Import Arista cEOS tarball. Returns success."""
    path = Path(tarball)
    if not path.is_file():
        _log.error("Arista tarball not found: %s", path)
        return False
    if _image_exists(tag, dry_run=dry_run):
        _log.info("Arista image %s already present", tag)
        return True

    _log.info("importing Arista cEOS from %s as %s …", path, tag)
    r = _run(
        ["podman", "load", "-i", str(path)],
        capture=True,
        check=False,
        dry_run=dry_run,
    )
    if r.returncode != 0:
        _log.error(
            "podman load failed for Arista: %s",
            r.stderr.strip(),
        )
        return False

    loaded_ref = _parse_loaded_image(r.stdout or "")
    if loaded_ref and loaded_ref != tag:
        _log.info("tagging %s -> %s", loaded_ref, tag)
        _run(
            ["podman", "tag", loaded_ref, tag],
            check=False,
            dry_run=dry_run,
        )

    _log.info("Arista cEOS imported: %s", tag)
    return True


def _import_cisco(
    tarball: str,
    tag: str,
    *,
    dry_run: bool,
) -> bool:
    """Import Cisco XRd tarball. Returns success."""
    path = Path(tarball)
    if not path.is_file():
        _log.error("Cisco XRd tarball not found: %s", path)
        return False
    if _image_exists(tag, dry_run=dry_run):
        _log.info("Cisco XRd image %s already present", tag)
        return True

    _log.info("importing Cisco XRd from %s …", path)
    if dry_run:
        _log.info("[dry-run] would import Cisco XRd as %s", tag)
        return True

    # Try podman load directly first.
    r = _run(
        ["podman", "load", "-i", str(path)],
        capture=True,
        check=False,
    )
    if r.returncode == 0:
        _log.info(
            "Cisco XRd loaded directly: %s",
            (r.stdout or "").strip(),
        )
        return True

    return _import_cisco_nested(path, tag)


def _import_cisco_nested(path: Path, tag: str) -> bool:
    """Try extracting a nested Docker archive from XRd tarball."""
    _log.info("direct load failed, trying nested extraction …")
    with tempfile.TemporaryDirectory(
        prefix="xrd-bootstrap-",
    ) as tmpdir:
        work = Path(tmpdir)
        try:
            with tarfile.open(path) as tf:
                inner_names = [
                    m.name
                    for m in tf.getmembers()
                    if m.name.endswith(
                        (".tar", ".tar.gz", ".tgz"),
                    )
                ]
                if not inner_names:
                    _log.error(
                        "no inner tar found in Cisco XRd archive",
                    )
                    return False
                _log.info(
                    "extracting inner archive: %s",
                    inner_names[0],
                )
                tf.extract(inner_names[0], work)
                inner_path = work / inner_names[0]
        except (tarfile.TarError, OSError) as exc:
            _log.error(
                "failed to extract Cisco XRd outer archive: %s",
                exc,
            )
            return False

        r = _run(
            ["podman", "load", "-i", str(inner_path)],
            capture=True,
            check=False,
        )
        if r.returncode != 0:
            _log.error(
                "podman load failed for Cisco XRd inner image: %s",
                r.stderr.strip(),
            )
            return False

    _log.info("Cisco XRd imported: %s", tag)
    return True


# ---------------------------------------------------------------------------
# Phase 5: Build GoBFD image
# ---------------------------------------------------------------------------


def _build_gobfd(*, dry_run: bool) -> bool:
    """Build GoBFD vendor-interop container image. Returns success."""
    containerfile = SCRIPT_DIR / "Containerfile.gobfd"
    if not containerfile.is_file():
        _log.error("Containerfile not found: %s", containerfile)
        return False

    _log.info("building GoBFD image from %s …", containerfile)
    try:
        _run(
            [
                "podman",
                "build",
                "-t",
                "gobfd-clab:latest",
                "-f",
                str(containerfile),
                str(PROJECT_ROOT),
            ],
            dry_run=dry_run,
        )
    except subprocess.CalledProcessError as exc:
        _log.error(
            "GoBFD image build failed (rc=%d)",
            exc.returncode,
        )
        return False

    _log.info("GoBFD image built: gobfd-clab:latest")
    return True


# ---------------------------------------------------------------------------
# Phase 6: Inventory report
# ---------------------------------------------------------------------------


def _print_inventory(
    tags: dict[str, str],
    *,
    dry_run: bool,
) -> int:
    """Print image inventory table. Returns count of missing images."""
    print(f"\n{_c.bold}Image Inventory:{_c.reset}")  # noqa: T201
    missing = 0
    for entry in INVENTORY:
        ref = entry["ref_tpl"].format(**tags)
        present = _image_exists(ref, dry_run=dry_run)
        source = entry["source"]
        if present or dry_run:
            mark = f"{_c.green}ready{_c.reset}"
            if source != "pull":
                mark += f" ({source})"
        else:
            mark = f"{_c.red}missing{_c.reset}"
            if source.startswith("--"):
                mark += f" ({source})"
            missing += 1
        label = entry["label"]
        print(f"  {label:<16s} {ref:<50s} {mark}")  # noqa: T201
    print()  # noqa: T201
    return missing


# ---------------------------------------------------------------------------
# Phase 7: Deploy / Test
# ---------------------------------------------------------------------------


def _run_deploy_or_test(
    *,
    deploy: bool,
    test: bool,
    dry_run: bool,
) -> int:
    """Run run.sh if requested. Returns exit code."""
    run_sh = SCRIPT_DIR / "run.sh"
    if not run_sh.is_file():
        _log.error("run.sh not found: %s", run_sh)
        return 1

    if deploy:
        cmd = [str(run_sh), "--up-only"]
    elif test:
        cmd = [str(run_sh)]
    else:
        return 0

    _log.info("delegating to run.sh: %s", " ".join(cmd))
    r = _run(cmd, check=False, dry_run=dry_run, cwd=str(SCRIPT_DIR))
    return r.returncode


# ---------------------------------------------------------------------------
# CLI
# ---------------------------------------------------------------------------


def _parse_args() -> argparse.Namespace:
    """Parse command-line arguments."""
    p = argparse.ArgumentParser(
        description="Bootstrap the GoBFD vendor interop lab.",
        formatter_class=argparse.RawDescriptionHelpFormatter,
        epilog=(
            "examples:\n"
            "  %(prog)s -v"
            "                          # full bootstrap\n"
            "  %(prog)s --deploy"
            "                    # bootstrap + deploy\n"
            "  %(prog)s --test"
            "                      # bootstrap + test\n"
            "  %(prog)s --arista-image cEOS.tar"
            "     # include Arista\n"
            "  %(prog)s --dry-run"
            "                   # preview actions\n"
            "  %(prog)s --skip-pull"
            "                 # skip existing\n"
        ),
    )

    g = p.add_argument_group("commercial images")
    g.add_argument(
        "--arista-image",
        metavar="PATH",
        help="path to cEOS tarball",
    )
    g.add_argument(
        "--arista-tag",
        metavar="TAG",
        default="ceos:4.35.2F",
        help="tag for imported Arista image (default: %(default)s)",
    )
    g.add_argument(
        "--cisco-image",
        metavar="PATH",
        help="path to XRd CP tarball",
    )
    g.add_argument(
        "--cisco-tag",
        metavar="TAG",
        default="ios-xr/xrd-control-plane:25.4.1",
        help="tag for imported Cisco image (default: %(default)s)",
    )

    g = p.add_argument_group("VyOS")
    g.add_argument(
        "--vyos-iso",
        metavar="PATH",
        help="path to local VyOS ISO (skip download)",
    )
    g.add_argument(
        "--vyos-version",
        metavar="VER",
        default="latest",
        help="VyOS rolling version (default: %(default)s)",
    )

    g = p.add_argument_group("image tags")
    g.add_argument(
        "--nokia-tag",
        metavar="TAG",
        default="25.10.2",
        help="Nokia SR Linux tag (default: %(default)s)",
    )
    g.add_argument(
        "--frr-tag",
        metavar="TAG",
        default="10.2.5",
        help="FRR tag (default: %(default)s)",
    )
    g.add_argument(
        "--sonic-tag",
        metavar="TAG",
        default="latest",
        help="SONiC-VS tag (default: %(default)s)",
    )

    g = p.add_argument_group("behaviour")
    g.add_argument(
        "--skip-build",
        action="store_true",
        help="skip GoBFD image build",
    )
    g.add_argument(
        "--skip-pull",
        action="store_true",
        help="skip pulling images that already exist locally",
    )
    g.add_argument(
        "--deploy",
        action="store_true",
        help="after preparation, run run.sh --up-only",
    )
    g.add_argument(
        "--test",
        action="store_true",
        help="after preparation, run full run.sh",
    )
    g.add_argument(
        "--dry-run",
        action="store_true",
        help="print what would be done without executing",
    )
    g.add_argument(
        "--jobs",
        metavar="N",
        type=int,
        default=3,
        help="max parallel image pulls (default: %(default)s)",
    )
    g.add_argument(
        "-v",
        "--verbose",
        action="store_true",
        help="debug logging",
    )

    return p.parse_args()


# ---------------------------------------------------------------------------
# Main orchestration
# ---------------------------------------------------------------------------


def _build_tags(args: argparse.Namespace) -> dict[str, str]:
    """Derive image tag substitution map from CLI args."""
    arista_tag = args.arista_tag.split(":")[-1] if ":" in args.arista_tag else "4.35.2F"
    cisco_tag = args.cisco_tag.split(":")[-1] if ":" in args.cisco_tag else "25.4.1"
    return {
        "nokia_tag": args.nokia_tag,
        "sonic_tag": args.sonic_tag,
        "frr_tag": args.frr_tag,
        "arista_tag": arista_tag,
        "cisco_tag": cisco_tag,
    }


def _run_phases(args: argparse.Namespace) -> list[str]:
    """Execute bootstrap phases 1-5. Returns list of failures."""
    failures: list[str] = []
    dry = args.dry_run
    tags = _build_tags(args)

    _log.info("%s--- Phase 1: Preflight checks ---%s", _c.bold, _c.reset)
    _preflight(dry)

    _log.info(
        "%s--- Phase 2: Pull open-source images ---%s",
        _c.bold,
        _c.reset,
    )
    resolved = {k: v.format(**tags) for k, v in OPEN_SOURCE_IMAGES.items()}
    results = _pull_images(
        resolved,
        max_workers=args.jobs,
        skip_pull=args.skip_pull,
        dry_run=dry,
    )
    failures.extend(f"pull:{n}" for n, ok in results.items() if not ok)

    _log.info(
        "%s--- Phase 3: Build VyOS image ---%s",
        _c.bold,
        _c.reset,
    )
    if not _build_vyos(
        vyos_iso=args.vyos_iso,
        vyos_version=args.vyos_version,
        skip_pull=args.skip_pull,
        dry_run=dry,
    ):
        failures.append("vyos")

    _log.info(
        "%s--- Phase 4: Import commercial images ---%s",
        _c.bold,
        _c.reset,
    )
    failures.extend(_import_commercial(args, dry_run=dry))

    _log.info(
        "%s--- Phase 5: Build GoBFD image ---%s",
        _c.bold,
        _c.reset,
    )
    if args.skip_build:
        _log.info("GoBFD build: skipped (--skip-build)")
    elif not _build_gobfd(dry_run=dry):
        failures.append("gobfd-build")

    _log.info(
        "%s--- Phase 6: Image inventory ---%s",
        _c.bold,
        _c.reset,
    )
    _print_inventory(tags, dry_run=dry)

    return failures


def _import_commercial(
    args: argparse.Namespace,
    *,
    dry_run: bool,
) -> list[str]:
    """Import any user-supplied commercial image tarballs."""
    failures: list[str] = []
    if args.arista_image:
        if not _import_arista(
            args.arista_image,
            args.arista_tag,
            dry_run=dry_run,
        ):
            failures.append("arista")
    else:
        _log.info("Arista cEOS: skipped (no --arista-image)")

    if args.cisco_image:
        if not _import_cisco(
            args.cisco_image,
            args.cisco_tag,
            dry_run=dry_run,
        ):
            failures.append("cisco")
    else:
        _log.info("Cisco XRd: skipped (no --cisco-image)")
    return failures


def main() -> int:
    """Entry point: parse args, run phases, report results."""
    args = _parse_args()

    _init_colours()

    handler = logging.StreamHandler(sys.stderr)
    handler.setFormatter(_Formatter())
    _log.addHandler(handler)
    _log.setLevel(logging.DEBUG if args.verbose else logging.INFO)

    failures = _run_phases(args)

    if failures:
        _log.error("failed steps: %s", ", ".join(failures))

    if args.deploy or args.test:
        if failures:
            _log.error(
                "skipping deploy/test due to earlier failures",
            )
            return 1
        _log.info(
            "%s--- Phase 7: Deploy / Test ---%s",
            _c.bold,
            _c.reset,
        )
        rc = _run_deploy_or_test(
            deploy=args.deploy,
            test=args.test,
            dry_run=args.dry_run,
        )
        if rc != 0:
            return rc

    return 1 if failures else 0


if __name__ == "__main__":
    raise SystemExit(main())
