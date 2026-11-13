#!/bin/sh
# Runs the built program inside the Parallels test VM and brings the
# results back. The repository stays on the Mac; only dist/ is shared
# (as \\Mac\spacehop).
#   tools/vm.sh selftest          run the self-test, print the report
#   tools/vm.sh ui                only photograph the settings window (sends no keys)
#   tools/vm.sh bench             time opening a program: directly and through a shortcut
#   tools/vm.sh shot [name]       screenshot of the VM into dist/vm/
#   tools/vm.sh run <args...>     run SpaceHop with arguments
#   tools/vm.sh cmd <command>     run a command in the user's session
set -eu
cd "$(dirname "$0")/.."
VM="${SPACEHOP_VM:-{8a841e72-4dfd-4bd7-be6e-e4b478b5f672}}"
ARCH="${SPACEHOP_VM_ARCH:-arm64}"
in_vm() { prlctl exec "$VM" --current-user "$@"; }
stage() {
  in_vm cmd /c "taskkill /F /IM SpaceHop.exe >nul 2>&1 & ping -n 2 127.0.0.1 >nul & (if not exist C:\\spacehop mkdir C:\\spacehop) & copy /Y \\\\Mac\\spacehop\\SpaceHop-$ARCH.exe C:\\spacehop\\SpaceHop.exe >nul"
}
case "${1:-selftest}" in
  selftest|ui)
    ONLY=""; [ "$1" = ui ] && ONLY=" ui"
    stage
    rm -rf dist/vm && mkdir -p dist/vm
    in_vm cmd /c "rmdir /S /Q C:\\spacehop\\out >nul 2>&1 & start /wait C:\\spacehop\\SpaceHop.exe --selftest C:\\spacehop\\out$ONLY & echo exit code %ERRORLEVEL% & xcopy /Y /Q C:\\spacehop\\out \\\\Mac\\spacehop\\vm\\ >nul"
    cat dist/vm/report.txt
    ;;
  bench)
    stage
    in_vm cmd /c "start /wait C:\\spacehop\\SpaceHop.exe --bench C:\\spacehop\\bench & copy /Y C:\\spacehop\\bench\\bench.txt \\\\Mac\\spacehop\\vm\\bench.txt >nul"
    cat dist/vm/bench.txt
    ;;
  shot)
    in_vm cmd /c "start /wait C:\\spacehop\\SpaceHop.exe --screenshot C:\\spacehop\\shot.png & copy /Y C:\\spacehop\\shot.png \\\\Mac\\spacehop\\vm\\${2:-shot}.png >nul"
    ;;
  run)
    # prlctl waits for the program it started, so leave it in the background.
    shift; stage
    in_vm cmd /c "start \"\" C:\\spacehop\\SpaceHop.exe $*" >/dev/null 2>&1 &
    sleep 3
    ;;
  cmd) shift; in_vm cmd /c "$*" ;;
esac
