# Add-on catalog

TECHO5's add-on catalog lives in its own repository,
[HuskerMinion/techo5-addons](https://github.com/HuskerMinion/techo5-addons). To get an add-on listed,
open its [Submit an add-on](https://github.com/HuskerMinion/techo5-addons/issues/new?template=submit-addon.yml)
form.

Devices still fetch the signed catalog from this repository's
[`addons` release](https://github.com/HuskerMinion/techo5/releases/tag/addons), which
`tools/sign-catalog.ps1` publishes from a techo5-addons checkout. How add-ons are installed and run is
in [docs/setup.md](../docs/setup.md#add-ons).
