# NetBird: reaching a device from away

NetBird is no longer built into TECHO5. It runs as an add-on,
[techo5-netbird](https://github.com/tioan/techo5-netbird), made and kept by its author: NetBird's own
client, started from userdata at every boot through
[rc.local](setup.md#running-something-of-your-own-at-boot), and set up over SSH. It and the other
add-ons are listed in [Add-ons others have made](external-asp.md#add-ons-others-have-made).

A device that ran the built-in NetBird turns it off on its first start after the update and deletes
the client it had downloaded. Its peer is kept, so the add-on can take it over (`techo5-netbird
migrate`) without a new setup key. TECHO5's firewall still lets NetBird's interface reach only Home
Assistant's encrypted link, the setup page while Remote setup is on, SSH if it is on, and ping. That rule, and the device treating a NetBird connection as from away, go by the interface name
`netbird0`, which the add-on keeps; a NetBird run under another interface name or in userspace mode
would be treated as part of the home network.
