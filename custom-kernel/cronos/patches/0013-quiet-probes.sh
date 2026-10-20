#!/bin/sh
# Keep expected probe failures out of the kernel log, so what is left there is worth reading.
set -eu
python3 - <<'PY'
import os
from pathlib import Path
k = Path(os.environ.get('KDIR', '/src/linux-7.2.11'))

# The MMC core finds out what is on a host by trying SDIO, SD and MMC in turn, and the commands for
# the kinds that are not there fail by design. Warn about failed commands only once a card is known.
p = k / 'drivers/mmc/host/mtk-sd.c'
s = p.read_text()
old = '''	if (host->error &&
	    ((!mmc_op_tuning(cmd->opcode) && !host->hs400_tuning) ||'''
new = '''	if (host->error && mmc_from_priv(host)->card &&
	    ((!mmc_op_tuning(cmd->opcode) && !host->hs400_tuning) ||'''
if new not in s:
    if old not in s:
        raise SystemExit('mtk-sd.c: msdc_track_cmd_data not found')
    p.write_text(s.replace(old, new, 1))

# A Goodix configuration file is optional: without one the controller keeps the configuration it
# was given at the factory, which is what it should do on this board.
p = k / 'drivers/input/touchscreen/goodix.c'
s = p.read_text()
old = 'error = request_firmware_nowait(THIS_MODULE, true, ts->cfg_name,'
new = 'error = firmware_request_nowait_nowarn(THIS_MODULE, ts->cfg_name,'
if new not in s:
    if old not in s:
        raise SystemExit('goodix.c: config request not found')
    p.write_text(s.replace(old, new, 1))
PY
