# Licenses of the firmware the ThinkSmart View's image ships

`mkstore.sh`, when it is not given the unit's own firmware (`-F`), puts two kinds of firmware into the
store from Alpine's packages, and these files beside them in the image:

| Firmware in the image | From | License, as shipped beside it |
|---|---|---|
| `lib/firmware/qcom/a530_pm4.fw`, `a530_pfp.fw`, `a530v3_gpmu.fw2`: the Adreno 506's microcode | linux-firmware (Alpine's `linux-firmware-qcom`); its WHENCE: "Redistributable. See LICENSE.qcom and NOTICE.qcom" | `lib/firmware/qcom/LICENSE.qcom`, `lib/firmware/qcom/NOTICE.qcom` |
| `lib/firmware/regulatory.db`, `regulatory.db.p7s`: the Wi-Fi regulatory database | wireless-regdb (Alpine's `wireless-regdb`) | `lib/firmware/LICENSE.wireless-regdb` (ISC) |

The texts are as in their projects: `LICENSE.qcom` and `NOTICE.qcom` from linux-firmware's `LICENSES/`
(git.kernel.org/pub/scm/linux/kernel/git/firmware/linux-firmware.git, commit 0cf5bba0db28), and
`LICENSE.wireless-regdb` from wireless-regdb's `LICENSE`
(git.kernel.org/pub/scm/linux/kernel/git/wens/wireless-regdb.git, commit 412d41068c46).

Everything else the unit runs on (the modem, Wi-Fi, Bluetooth and the GPU's zap shader) is the unit's
own, made at every boot from its partitions by `thinksmart-firmware`, and is not in the image.
