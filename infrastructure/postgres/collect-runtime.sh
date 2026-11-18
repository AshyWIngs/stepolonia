#!/bin/sh
set -eu

runtime_root=/runtime
mkdir -p "$runtime_root/usr/libexec/postgresql18" "$runtime_root/lib/apk/db" "$runtime_root/etc/ssl/certs"
# Keep tools that need a shell (initdb, pg_ctl, pg_dumpall) in the bootstrap image.
for binary in postgres psql pg_dump pg_restore pg_isready pg_controldata; do
    cp -a "/usr/libexec/postgresql18/$binary" "$runtime_root/usr/libexec/postgresql18/"
done
# Retain contrib, PL/pgSQL, LLVM JIT, dictionaries, and ICU data.
cp -a --parents /usr/lib/postgresql18 /usr/share/postgresql18 /usr/share/icu "$runtime_root/"
cp -a --parents /usr/lib/engines-3 /usr/lib/ossl-modules "$runtime_root/"
cp -a --parents /usr/share/zoneinfo /usr/share/licenses "$runtime_root/"
cp -a --parents /etc/os-release /etc/alpine-release /etc/ssl/openssl.cnf \
    /etc/ssl/cert.pem /etc/ssl/certs/ca-certificates.crt "$runtime_root/"
# Alpine's os-release can be a symlink to /usr/lib/os-release.
cp -a --parents /usr/lib/os-release "$runtime_root/"
printf 'PostgreSQL %s\n' "$PG_VERSION" > "$runtime_root/usr/share/postgresql18/source-version"

{
    find "$runtime_root/usr/libexec/postgresql18" -type f
    find "$runtime_root/usr/lib" -type f \( -name '*.so' -o -name '*.so.*' \)
} | sort > /tmp/runtime-elf-files
: > /tmp/runtime-ldd
while IFS= read -r file; do
    file=${file#"$runtime_root"}
    # musl reports unresolved server symbols when loading PostgreSQL modules alone.
    # A missing library always fails the build.
    if ! ldd "$file" > /tmp/runtime-ldd-one 2>&1; then
        case "$file" in
            /usr/libexec/postgresql18/*) cat /tmp/runtime-ldd-one >&2; exit 1 ;;
            *) if grep -Ev '^[[:space:]]|^Error relocating .*: .*: symbol not found$' /tmp/runtime-ldd-one; then
                   cat /tmp/runtime-ldd-one >&2; exit 1
               fi ;;
        esac
    fi
    if grep -Eq 'Error loading shared library|=>.*not found|No such file' /tmp/runtime-ldd-one; then
        cat /tmp/runtime-ldd-one >&2
        exit 1
    fi
    cat /tmp/runtime-ldd-one >> /tmp/runtime-ldd
done < /tmp/runtime-elf-files
awk '/=> \// {print $3} /^[[:space:]]*\// {print $1}' /tmp/runtime-ldd \
    | sort -u > /tmp/runtime-libraries
while IFS= read -r library; do
    cp -a --parents "$library" "$runtime_root/"
    cp -a --parents "$(readlink -f "$library")" "$runtime_root/"
done < /tmp/runtime-libraries

# Keep original APK versions, licenses, and checksums, with file lists limited
# to files copied to the runtime image.
find "$runtime_root" \( -type f -o -type l \) | sed "s|^$runtime_root/||" | sort > /tmp/runtime-files
awk '
    FILENAME == ARGV[1] { copied[$0] = 1; next }
    {
        header = ""; files = ""; directory = ""
        count = split($0, lines, "\n")
        for (i = 1; i <= count; i++) {
            line = lines[i]
            if (line ~ /^F:/) { directory = substr(line, 3); continue }
            if (line ~ /^R:/) {
                path = directory "/" substr(line, 3)
                selected = (path in copied)
                if (selected) files = files "F:" directory "\n" line "\n"
                while (i < count && lines[i + 1] !~ /^[FR]:/) {
                    i++
                    if (selected) files = files lines[i] "\n"
                }
            } else if (directory == "") {
                header = header line "\n"
            }
        }
        if (files != "") printf "%s%s\n", header, files
    }
' /tmp/runtime-files RS='' /lib/apk/db/installed > "$runtime_root/lib/apk/db/installed"

printf 'root:x:0:0:root:/root:/sbin/nologin\npostgres:x:999:999:PostgreSQL:/var/lib/postgresql:/sbin/nologin\n' > "$runtime_root/etc/passwd"
printf 'root:x:0:\npostgres:x:999:\n' > "$runtime_root/etc/group"
install -d -o 999 -g 999 -m 1777 "$runtime_root/var/lib/postgresql"
install -d -o 999 -g 999 -m 3777 "$runtime_root/run/postgresql"
ln -s /run "$runtime_root/var/run"
install -d -m 1777 "$runtime_root/tmp"
# Run the loader check against the assembled filesystem.
chroot "$runtime_root" /usr/libexec/postgresql18/postgres --version
while IFS= read -r library; do
    test -e "$runtime_root$library"
done < /tmp/runtime-libraries
