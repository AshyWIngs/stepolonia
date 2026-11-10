#!/usr/bin/env python3
"""Create one installation's local configuration without replacing credentials."""
import argparse
import json
import os
import secrets

from site_config import load_config, site_directory


def setup(site, port=None, name=None):
    root = site_directory(site)
    root.mkdir(parents=True, exist_ok=True)
    env = root / ".env"
    if not env.exists():
        example = root / ".env.example"
        if example.exists():
            text = example.read_text()
        else:
            project = "sp-" + site.replace(".", "_")
            values = {
                "SITE_ID": site, "COMPOSE_PROJECT_NAME": project,
                "HTTP_BIND": "127.0.0.1", "HTTP_PORT": "8080",
                "POSTGRES_DB": "portal", "POSTGRES_USER": "portal_owner", "DB_USER": "portal_app",
                "DB_VOLUME_NAME": project + "_postgres_data", "DB_VOLUME_EXTERNAL": "false",
                "NUXT_PUBLIC_SITE_NAME": site, "NUXT_PUBLIC_SITE_LOCALE": "ru",
                "NUXT_PUBLIC_SITE_TAGLINE": "", "NUXT_PUBLIC_SITE_DESCRIPTION": "",
                "NUXT_PUBLIC_SITE_NOTICE": "", "NUXT_PUBLIC_SITE_ACCENT": "#a52837",
            }
            text = "".join(f"{key}={json.dumps(value, ensure_ascii=False)}\n" for key, value in values.items())
        if port is not None:
            text += f"\nHTTP_PORT={port}\n"
        if name is not None:
            text += "\nNUXT_PUBLIC_SITE_NAME=" + json.dumps(name, ensure_ascii=False) + "\n"
        fd = os.open(env, os.O_WRONLY | os.O_CREAT | os.O_EXCL, 0o600)
        with os.fdopen(fd, "w") as output:
            output.write(text)
    # Validate the selected installation before creating or touching its secrets.
    load_config(site)
    directory = root / ".secrets"
    directory.mkdir(mode=0o700, exist_ok=True)
    os.chmod(directory, 0o700)
    for secret_name in ("db_owner_password", "db_app_password"):
        path = directory / secret_name
        if not path.exists():
            fd = os.open(path, os.O_WRONLY | os.O_CREAT | os.O_EXCL, 0o600)
            with os.fdopen(fd, "w") as output:
                output.write(secrets.token_urlsafe(32) + "\n")
        # Compose retains host permissions; the parent directory is owner-only.
        os.chmod(path, 0o444)
    print(f"Configuration ready: {site}; existing settings and credentials were preserved.")


if __name__ == "__main__":
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--site", required=True)
    parser.add_argument("--port", type=int)
    parser.add_argument("--name")
    args = parser.parse_args()
    if args.port is not None and not 1024 <= args.port <= 65535:
        parser.error("--port must be between 1024 and 65535")
    setup(args.site, args.port, args.name)
