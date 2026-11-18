#!/usr/bin/env python3
"""Run a local smoke command with a disposable builder and optional kind cluster."""
import argparse
from contextlib import ExitStack
import os
from pathlib import Path
import secrets
import signal
import subprocess
import tempfile

from site_config import ROOT


def tool(*args, environment, capture=False):
    result = subprocess.run(args, cwd=ROOT, env=environment, check=True, text=True,
                            stdout=subprocess.PIPE if capture else None)
    return result.stdout.strip() if capture else None


def local_docker():
    """Pin the local Docker context for the entire run, including teardown."""
    environment = dict(os.environ)
    if environment.pop("DOCKER_HOST", None) and not environment.get("DOCKER_CONTEXT"):
        raise RuntimeError("Select the local Docker context explicitly instead of DOCKER_HOST")
    context = tool("docker", "context", "show", environment=environment, capture=True)
    endpoint = tool("docker", "context", "inspect", context, "--format",
                    "{{.Endpoints.docker.Host}}", environment=environment, capture=True)
    if not endpoint.startswith("unix:///"):
        raise RuntimeError("Smoke tests require a local Docker Unix socket")
    environment["DOCKER_CONTEXT"] = context
    return environment


def add_builder(resources, name, environment):
    existing = tool("docker", "buildx", "ls", "--format", "{{.Name}}",
                    environment=environment, capture=True)
    if name in existing.splitlines():
        raise RuntimeError(f"Builder already exists: {name}")
    tool("docker", "buildx", "create", "--name", name, "--driver", "docker-container",
         environment["DOCKER_CONTEXT"], environment=environment)
    resources.callback(tool, "docker", "buildx", "rm", name, environment=environment)
    environment["BUILDX_BUILDER"] = name


def add_cluster(resources, name, directory, environment):
    environment.update(KUBECONFIG=str(directory / "kubeconfig"), KIND_EXPERIMENTAL_PROVIDER="docker")
    clusters = tool("kind", "get", "clusters", environment=environment, capture=True)
    if name in clusters.splitlines():
        raise RuntimeError(f"kind cluster already exists: {name}")
    network = tool("docker", "network", "create", "--label", f"io.stepolonia.smoke={name}", name,
                   environment=environment, capture=True)
    resources.callback(tool, "docker", "network", "rm", network, environment=environment)
    environment.update(KIND_EXPERIMENTAL_DOCKER_NETWORK=name, STEPOLONIA_SMOKE_CLUSTER=name)
    resources.callback(tool, "kind", "delete", "cluster", "--name", name, environment=environment)
    tool("kind", "create", "cluster", "--name", name, "--kubeconfig", environment["KUBECONFIG"],
         "--wait", "120s", environment=environment)


def run_command(command, environment):
    """Stop the complete command group on interruption before removing its dependencies."""
    with subprocess.Popen(command, cwd=ROOT, env=environment, start_new_session=True) as process:
        try:
            return process.wait()
        except KeyboardInterrupt:
            if process.poll() is None:
                try:
                    os.killpg(process.pid, signal.SIGTERM)
                    process.wait(timeout=30)
                except subprocess.TimeoutExpired:
                    os.killpg(process.pid, signal.SIGKILL)
                    process.wait()
                except ProcessLookupError:
                    process.wait()
            raise


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--kind", action="store_true", help="start a local kind cluster")
    parser.add_argument("command", nargs=argparse.REMAINDER)
    options = parser.parse_args()
    command = options.command
    if command[:1] == ["--"]:
        command = command[1:]
    if not command:
        parser.error("supply a smoke command after --")

    def interrupted(_signal, _frame):
        raise KeyboardInterrupt

    signal.signal(signal.SIGTERM, interrupted)
    environment = local_docker()
    name = f"sp-smoke-{secrets.token_hex(8)}"
    print(f"Local smoke run: {name}", flush=True)
    with tempfile.TemporaryDirectory(prefix=f"{name}-") as scratch, ExitStack() as resources:
        directory = Path(scratch)
        environment["STEPOLONIA_SMOKE_TMP"] = scratch
        add_builder(resources, name, environment)
        if options.kind:
            add_cluster(resources, name, directory, environment)
        return run_command(command, environment)


if __name__ == "__main__":
    try:
        raise SystemExit(main())
    except KeyboardInterrupt:
        raise SystemExit(130) from None
