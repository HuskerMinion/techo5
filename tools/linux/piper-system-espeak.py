"""Point libpiper at the espeak-ng Alpine already has, instead of building its own.

Upstream builds espeak-ng as a CMake ExternalProject: cmake driving cmake driving a download and a
build. None of that survives QEMU user emulation, where cmake cannot fork a child at all. Alpine
ships espeak-ng for armv7 with headers, so the whole external project can go.

    python3 piper-system-espeak.py <path to libpiper/CMakeLists.txt>
"""

import re
import sys


def main() -> int:
    if len(sys.argv) != 2:
        print(__doc__, file=sys.stderr)
        return 2
    path = sys.argv[1]
    s = open(path, encoding="utf-8").read()
    if "patched by piper-system-espeak.py" in s:
        print("already patched")
        return 0

    # The external project, from its opening line to the closing paren on its own line.
    start = s.index("ExternalProject_Add(espeak_ng_external")
    end = s.index("\n)\n", start) + len("\n)\n")
    s = s[:start] + "# espeak-ng comes from the system (patched by piper-system-espeak.py)\n" + s[end:]

    # What it would have built becomes what Alpine installs. ucd is inside espeak-ng's shared library.
    s = re.sub(
        r"set\(ESPEAKNG_STATIC_LIB [^\)]*\)",
        "find_library(ESPEAKNG_STATIC_LIB NAMES espeak-ng REQUIRED)",
        s,
    )
    s = re.sub(r"set\(UCD_STATIC_LIB [^\)]*\)", 'set(UCD_STATIC_LIB "")', s)
    s = s.replace("add_dependencies(piper espeak_ng_external)\n", "")
    s = s.replace("include(ExternalProject)\n", "")
    s = s.replace("${ESPEAKNG_INSTALL_DIR}/include", "/usr/include")

    open(path, "w", encoding="utf-8").write(s)
    print("patched", path)
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
