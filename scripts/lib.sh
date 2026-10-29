#!/bin/sh
# Funções compartilhadas. Os outros scripts fazem source deste arquivo.

use_npm11() {
	if command -v npm >/dev/null 2>&1; then
		case "$(npm -v 2>/dev/null)" in
		11.*) return 0 ;;
		esac
	fi
	old_ifs=$IFS
	IFS=:
	# shellcheck disable=SC2086
	set -- $PATH
	IFS=$old_ifs
	for dir in "$@" "${HOME}/.local/share/node24/bin"; do
		if [ -n "$dir" ] && [ -x "$dir/npm" ]; then
			case "$("$dir/npm" -v 2>/dev/null)" in
			11.*)
				PATH="$dir:$PATH"
				export PATH
				hash -r 2>/dev/null || true
				return 0
				;;
			esac
		fi
	done
	echo "npm 11 não encontrado. Use o npm que acompanha o Node 24." >&2
	return 1
}
