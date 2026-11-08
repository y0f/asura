#!/usr/bin/env bash
# Exercise the watcher with fake tools and a disposable server; no Go build.
set -euo pipefail
SOURCE=$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")/.." && pwd)
FIXTURE=$(mktemp -d)
WATCHER=""
cleanup() {
    if [[ -n "$WATCHER" ]]; then
        kill "$WATCHER" 2>/dev/null || true
        wait "$WATCHER" 2>/dev/null || true
    fi
    rm -rf -- "$FIXTURE"
}
trap cleanup EXIT
cp "$SOURCE/dev.sh" "$FIXTURE/dev.sh"
cd "$FIXTURE"
mkdir -p cmd internal web/static bin
printf 'module fixture\nrequire github.com/a-h/templ v0.3.1100\n' > go.mod
touch go.sum
printf 'first\n' > web/tailwind.input.css
printf 'template-first\n' > internal/component.templ
cat > bin/go <<'GO'
#!/usr/bin/env bash
set -euo pipefail
case "$1" in
    list) awk '$2 == "github.com/a-h/templ" {print $3}' go.mod ;;
    run)
        expected=$(awk '$2 == "github.com/a-h/templ" {print $3}' go.mod)
        [[ "$2" == "github.com/a-h/templ/cmd/templ@$expected" && "$3" == generate ]]
        echo "$expected" >> versions
        echo templ >> steps
        [[ ! -f fail-templ ]]
        captured=$(cat internal/component.templ)
        if [[ -f hold-templ ]]; then
            echo started > generation.started
            while [[ -f hold-templ ]]; do sleep 0.1; done
        fi
        printf '%s\n' "$captured" > internal/component_templ.go
        ;;
    build)
        echo build >> steps
        [[ ! -f fail-build ]]
        [[ "$2" == -o ]]
        cp web/static/tailwind.css built.css
        cp internal/component_templ.go built.templ
        cat > "$3" <<'SERVER'
#!/usr/bin/env bash
trap 'exit 0' TERM INT
printf '%s\n' "$$" > server.pid
cat built.css >> starts
cat built.templ >> templates
while sleep 0.1; do :; done
SERVER
        chmod +x "$3"
        ;;
    *) exit 90 ;;
esac
GO
cat > tailwindcss <<'TW'
#!/usr/bin/env bash
set -euo pipefail
echo tailwind >> steps
[[ ! -f fail-css ]]
cp web/tailwind.input.css web/static/tailwind.css
TW
chmod +x bin/go tailwindcss
export PATH="$FIXTURE/bin:$PATH"
bash dev.sh > dev.log 2>&1 &
WATCHER=$!

wait_for() {
    local pattern=$1 file=$2
    for ((attempt=0; attempt<100; attempt++)); do
        if [[ -f "$file" ]] && grep -q "$pattern" "$file"; then return 0; fi
        kill -0 "$WATCHER" 2>/dev/null || { cat dev.log; return 1; }
        sleep 0.1
    done
    cat dev.log
    echo "Timed out waiting for $pattern in $file" >&2
    return 1
}

wait_for '^first$' starts
[[ "$(cat steps)" == $'templ\ntailwind\nbuild' ]]
echo 'PASS generation finishes before the first embedded build'

printf 'second\n' > web/tailwind.input.css
wait_for '^second$' starts
echo 'PASS CSS-only changes rebuild and restart the server'

before=$(wc -l < starts)
printf '<svg/>\n' > web/static/fixture.svg
for ((attempt=0; attempt<100; attempt++)); do
    [[ "$(wc -l < starts)" -gt "$before" ]] && break
    sleep 0.1
done
[[ "$(wc -l < starts)" -eq $((before+1)) ]]
echo 'PASS embedded asset changes rebuild the server'

for failure in templ css build; do
    prior_pid=$(cat server.pid)
    before=$(wc -l < starts)
    : > "fail-$failure"
    : > dev.log
    printf '%s\n' "$failure" > web/tailwind.input.css
    wait_for 'build failed; keeping the previous server' dev.log
    kill -0 "$prior_pid"
    [[ "$(wc -l < starts)" -eq "$before" ]]
    rm "fail-$failure"
    echo "PASS $failure failure preserves the running server"
done

printf 'recovered\n' > web/tailwind.input.css
wait_for '^recovered$' starts
echo 'PASS a later edit recovers after build failure'

printf 'module fixture\nrequire github.com/a-h/templ v0.3.1101\n' > go.mod
wait_for '^v0.3.1101$' versions
echo 'PASS changing go.mod updates the templ CLI version'

touch hold-templ
printf 'during-generation\n' > web/tailwind.input.css
wait_for '^' generation.started
printf 'edited-during-generation\n' > internal/component.templ
rm hold-templ
wait_for '^edited-during-generation$' templates
echo 'PASS edits during generation trigger a subsequent complete build'
last_pid=$(cat server.pid)
kill -TERM "$WATCHER"
wait "$WATCHER" || [[ "$?" -eq 143 ]]
WATCHER=""
! kill -0 "$last_pid" 2>/dev/null
[[ ! -e asura-dev && ! -e asura-dev.next ]]
echo 'PASS shutdown stops the owned server and removes temporary binaries'
