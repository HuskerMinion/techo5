"""Apply checked, minimal call-site changes to the pinned soloistd build clone."""
from pathlib import Path
import sys

root = Path(sys.argv[1])
replacements = {
    'download.go': [('exec.CommandContext(checkCtx, tmpBinary, "--version")',
                     'soloistCommand(checkCtx, tmpBinary, "--version")')],
    'commands.go': [('exec.CommandContext(ctx, p.soloistBinary(),',
                     'soloistCommand(ctx, p.soloistBinary(),')],
    'supervisor.go': [('exec.Command(binary, args...)',
                       'soloistCommand(context.Background(), binary, args...)')],
}
for name, changes in replacements.items():
    path = root / name
    source = path.read_text()
    for old, new in changes:
        expected = 2 if name == 'commands.go' else 1
        if source.count(old) != expected:
            raise SystemExit(f'{name}: upstream call sites changed; refusing to patch')
        source = source.replace(old, new)
    # These files no longer construct commands themselves (supervisor still uses exec.Cmd).
    if name == 'download.go':
        source = source.replace('\n\t"os/exec"', '')
    path.write_text(source)
