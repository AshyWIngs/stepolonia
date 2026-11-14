#!/bin/bash
set -Eeuo pipefail

# Сохраняем PostgreSQL, bundled extensions/JIT и их ELF-зависимости.
# Пакетные метаданные нужны сканерам и для библиотек, скопированных вручную.
root=/runtime
mkdir -p "$root/usr/lib/postgresql/18/bin" "$root/var/lib/dpkg/status.d"
# pg_dumpall/pg_ctl/initdb запускают shell; они доступны в bootstrap-образе.
for binary in postgres psql pg_dump pg_restore pg_isready pg_controldata; do
    cp "/usr/lib/postgresql/18/bin/$binary" "$root/usr/lib/postgresql/18/bin/"
done
cp -a --parents /usr/lib/postgresql/18/lib /usr/share/postgresql/18 "$root/"
rm -rf "$root/usr/lib/postgresql/18/lib/pgxs"
cp -a --parents /usr/lib/locale /usr/lib/*-linux-gnu/gconv "$root/"
mkdir -p "$root/etc/ssl"
cp /etc/ssl/openssl.cnf "$root/etc/ssl/"

find "$root/usr/lib/postgresql/18" -type f \( -name '*.so' -o -path '*/bin/*' \) -print0 \
    | while IFS= read -r -d '' file; do
        ldd "$file"
    done > /tmp/runtime-ldd
if grep -q 'not found' /tmp/runtime-ldd; then
    cat /tmp/runtime-ldd >&2
    exit 1
fi
awk '/=> \// {print $3} /^[[:space:]]*\// {print $1}' /tmp/runtime-ldd \
    | while IFS= read -r library; do
        # Сохраняем SONAME (например, libpq.so.5), нормализуя только каталог.
        printf '%s/%s\n' "$(readlink -f "$(dirname "$library")")" "$(basename "$library")"
    done \
    | sort -u > /tmp/runtime-libraries
while IFS= read -r library; do
    cp -L --parents "$library" "$root/"
done < /tmp/runtime-libraries

{
    printf '%s\n' locales libc-l10n postgresql-18 postgresql-client-18
    while IFS= read -r file; do dpkg-query -S "$(readlink -f "$file")"; done < /tmp/runtime-libraries \
        | sed 's/: .*//'
} | sort -u > /tmp/runtime-packages
while IFS= read -r package; do
    dpkg-query -s "$package" > "$root/var/lib/dpkg/status.d/${package%%:*}"
    copyright="/usr/share/doc/${package%%:*}/copyright"
    if [[ -f "$copyright" ]]; then
        # Distroless содержит symlink-каталоги в /usr/share/doc.
        install -D -m 0644 "$copyright" "$root/usr/share/licenses/postgresql-runtime/${package%%:*}/copyright"
    fi
done < /tmp/runtime-packages

printf 'root:x:0:0:root:/root:/sbin/nologin\npostgres:x:999:999:PostgreSQL:/var/lib/postgresql:/sbin/nologin\n' > "$root/etc/passwd"
printf 'root:x:0:\npostgres:x:999:\n' > "$root/etc/group"
install -d -o 999 -g 999 -m 1777 "$root/var/lib/postgresql"
install -d -o 999 -g 999 -m 3777 "$root/var/run/postgresql"
