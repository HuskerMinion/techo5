# Page reader

A headless Chromium for the voice assistant, when it answers [directly](../../docs/setup.md#voice-without-home-assistant).
Some pages have no text until their scripts run, like a store's hours, and some sites turn away a
plain request. The device reads those pages through this instead: it loads the page the way a
browser does and sends the finished page back, and the device takes the text from it as usual.
It is only asked when a page has almost no text as it comes, or the site answers the device with
an error.

## Run it

On any machine with Docker, in this folder:

```
docker build -t techo5-reader .
docker run -d --name reader --restart unless-stopped --init --cap-add NET_ADMIN \
  --sysctl net.ipv6.conf.all.disable_ipv6=1 --memory 1g --shm-size 512m --cpus 1 --pids-limit 256 \
  -p 8891:8891 techo5-reader
```

`--init` reaps the browser's leftover processes, and `--cpus` and `--pids-limit` keep a page that
misbehaves from taking the whole machine.

Then set **Page reader** on the setup page (**Sound & Voice**, **Voice assistant**) to
`http://<that machine>:8891`. `curl http://<that machine>:8891/health` answers `ok`.

## Keeping it off your network

A page the assistant reads was found on the web, and a page can send a browser anywhere: to your
router's page, or another device at home. So the container starts with a firewall (`entrypoint.sh`)
that lets the browser reach the internet and the DNS server only, never private addresses
(192.168.x.y, 10.x, 172.16-31.x), the shared 100.64.0.0/10 range, or the machine itself. That is what
`--cap-add NET_ADMIN` is for: the firewall is set as root, and then the server runs as an ordinary
user that cannot change it. IPv6 is turned off in the container; if it is on and its firewall
cannot be set, the reader does not start.

Your own public address is not private, so the firewall does not know it, and a router can show its
admin page there. Add it, and anything else to keep the browser off, with `READER_BLOCK` (addresses
or ranges, separated by spaces or commas):

```
docker run ... -e READER_BLOCK="203.0.113.7" ... techo5-reader
```

Or let the reader look it up when it starts, with `-e READER_BLOCK_PUBLIC_IP=1`. That asks an outside
service for it (`https://api.ipify.org`, or the address in `READER_PUBLIC_IP_URL`, which must answer
with the address as plain text), so it is off unless you set it, and if the lookup fails the reader
does not start. A public address that changes is only picked up when the container restarts.

It works on Docker's default network and on a compose or other user-made network.

The reader has no password. Keep its port on your own network, as you would the search server, and
never forward it or publish it to the internet.

It uses about 100 MB of memory while idle, more while a page loads (up to the 1 GB set above), and
a few seconds per page. Two pages are read at once at most; a third gets "busy". A page that has
not been read in 25 seconds is given up on, and its browser closed if it will not let go.

## Credits

The reader runs [Playwright](https://playwright.dev) (Apache-2.0) and the
[Chromium](https://www.chromium.org) browser it brings, from Microsoft's Playwright image.
