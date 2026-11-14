#!/bin/sh
set -eu

root=/runtime
mkdir -p "$root/usr/libexec/postgresql18" "$root/lib/apk/db" "$root/etc/ssl/certs"
# Утилиты, которым нужен shell (initdb, pg_ctl, pg_dumpall), остаются в bootstrap.
for binary in postgres psql pg_dump pg_restore pg_isready pg_controldata; do
    cp -a "/usr/libexec/postgresql18/$binary" "$root/usr/libexec/postgresql18/"
done
# Сохраняем contrib, PL/pgSQL, LLVM JIT, словари и ICU-данные.
cp -a --parents /usr/lib/postgresql18 /usr/share/postgresql18 /usr/share/icu "$root/"
cp -a --parents /usr/lib/engines-3 /usr/lib/ossl-modules "$root/"
cp -a --parents /usr/share/zoneinfo /usr/share/licenses "$root/"
cp -a --parents /etc/os-release /etc/alpine-release /etc/ssl/openssl.cnf \
    /etc/ssl/cert.pem /etc/ssl/certs/ca-certificates.crt "$root/"
# os-release в Alpine может ссылаться на /usr/lib/os-release.
cp -a --parents /usr/lib/os-release "$root/"
printf 'PostgreSQL %s\n' "$PG_VERSION" > "$root/usr/share/postgresql18/source-version"

{
    find "$root/usr/libexec/postgresql18" -type f
    find "$root/usr/lib" -type f \( -name '*.so' -o -name '*.so.*' \)
} | sort > /tmp/runtime-elf-files
: > /tmp/runtime-ldd
while IFS= read -r file; do
    file=${file#"$root"}
    # musl сообщает о символах сервера у отдельно загруженных PG-модулей;
    # отсутствие самой библиотеки всегда является ошибкой сборки.
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
    cp -a --parents "$library" "$root/"
    cp -a --parents "$(readlink -f "$library")" "$root/"
done < /tmp/runtime-libraries

# APK-метаданные сохраняют исходные версии, лицензии и контрольные суммы,
# но перечисляют только действительно скопированные файлы пакетов.
find "$root" \( -type f -o -type l \) | sed "s|^$root/||" | sort > /tmp/runtime-files
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
' /tmp/runtime-files RS='' /lib/apk/db/installed > "$root/lib/apk/db/installed"

printf 'root:x:0:0:root:/root:/sbin/nologin\npostgres:x:999:999:PostgreSQL:/var/lib/postgresql:/sbin/nologin\n' > "$root/etc/passwd"
printf 'root:x:0:\npostgres:x:999:\n' > "$root/etc/group"
install -d -o 999 -g 999 -m 1777 "$root/var/lib/postgresql"
install -d -o 999 -g 999 -m 3777 "$root/run/postgresql"
ln -s /run "$root/var/run"
install -d -m 1777 "$root/tmp"
# Проверка загрузчика использует только собранную файловую систему.
chroot "$root" /usr/libexec/postgresql18/postgres --version
while IFS= read -r library; do
    test -e "$root$library"
done < /tmp/runtime-libraries
