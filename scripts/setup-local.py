#!/usr/bin/env python3
"""Create one installation's local configuration without replacing credentials."""
import argparse
import json
import os
import secrets
import stat

from site_config import load_config, site_directory


def setup_secrets(root):
    """Меняем права только проверенных inode, не переходя по ссылкам."""
    root_fd = os.open(root, os.O_RDONLY | os.O_DIRECTORY | os.O_NOFOLLOW)
    try:
        try:
            os.mkdir(".secrets", mode=0o700, dir_fd=root_fd)
        except FileExistsError:
            pass
        directory_fd = os.open(".secrets", os.O_RDONLY | os.O_DIRECTORY | os.O_NOFOLLOW,
                               dir_fd=root_fd)
        try:
            if os.fstat(directory_fd).st_uid != os.geteuid():
                raise ValueError("Каталог секретов должен принадлежать текущему пользователю")
            os.fchmod(directory_fd, 0o700)
            for name in ("db_owner_password", "db_app_password"):
                created = False
                try:
                    fd = os.open(name, os.O_WRONLY | os.O_CREAT | os.O_EXCL | os.O_NOFOLLOW,
                                 0o600, dir_fd=directory_fd)
                    created = True
                except FileExistsError:
                    # NONBLOCK исключает зависание на FIFO до проверки типа inode.
                    fd = os.open(name, os.O_RDONLY | os.O_NOFOLLOW | os.O_NONBLOCK,
                                 dir_fd=directory_fd)
                try:
                    metadata = os.fstat(fd)
                    if (not stat.S_ISREG(metadata.st_mode) or metadata.st_nlink != 1
                            or metadata.st_uid != os.geteuid()):
                        raise ValueError("Секрет должен быть обычным файлом владельца без жёстких ссылок")
                    if created:
                        with os.fdopen(os.dup(fd), "w") as output:
                            output.write(secrets.token_urlsafe(32) + "\n")
                    # Bind mount доступен non-root UID контейнера; на хосте каталог 0700.
                    os.fchmod(fd, 0o444)
                finally:
                    os.close(fd)
        finally:
            os.close(directory_fd)
    finally:
        os.close(root_fd)


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
    # Проверяем выбранную установку до создания и изменения секретов.
    load_config(site)
    setup_secrets(root)
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
