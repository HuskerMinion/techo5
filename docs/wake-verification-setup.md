# Set up wake verification

**Always Verify with STT** checks a locally detected wake word with Home Assistant speech-to-text before the satellite chimes or shows that it is listening. It uses the speech-to-text provider from the Assist pipeline selected for that wake word. Set up each satellite in Home Assistant before turning the switch on.

## 1. Check the Assist pipeline

Open the satellite's device page under **Settings → Devices & services → ESPHome**. Check which Assist pipeline is selected for the wake word you use. In **Settings → Voice assistants**, verify that this pipeline has a working speech-to-text provider.

## 2. Create a Home Assistant token

1. In Home Assistant, open **your profile → Security**.
2. Under **Long-lived access tokens**, select **Create token**. Give it a name that identifies the satellite, such as `TECHO5 living room`.
3. Copy the token while Home Assistant displays it. Keep it private and paste it only into the setup action below. [Home Assistant account security](https://www.home-assistant.io/docs/authentication/#managing-account-access)

## 3. Send the URL and token to the satellite

1. Open **Developer Tools → Actions**.
2. Find the satellite's `esphome.<device>_home_assistant` action. For example, a satellite with the ESPHome node name `office` exposes `esphome.office_home_assistant`.
3. Enter the Home Assistant base URL in **url** and paste the token into **token**. Use an address the satellite can reach, including `http://` or `https://` and the port if needed. A trailing slash is optional. For a default local installation, the format is `http://homeassistant.local:8123`.
4. Select **Perform action**.

## 4. Enable and test

1. On the satellite's device page, turn on **Always Verify with STT**.
2. Say the configured wake word by itself. After the chime, speak a short command. On a Show or Spot, the listening screen should appear with the chime.
3. On firmware with counter support, check **Wake STT requests sent**, **Wake STT allowed**, and **Wake STT blocked** under **Sensors**. An approved wake increases **Requests sent** and **Allowed**. A mismatch or no recognized speech increases **Requests sent** and **Blocked**. A request that was sent but failed before a verdict increases only **Requests sent**.

## If the satellite hears the wake word but stays silent

- Turn **Always Verify with STT** off to restore normal local wake handling while you check the setup.
- Confirm that you ran the action for this satellite and used a reachable Home Assistant URL and a valid token. If you replaced the token, run the same action again.
- Check that the microphone is unmuted, that the intended wake word is selected, and that its Assist pipeline can transcribe a spoken command. Then turn verification back on and try again.

## If the verification switch stays off

Firmware with the setup check refuses to enable verification until the satellite has a saved Home Assistant URL and token. If the ESPHome integration allows the device to perform Home Assistant actions, the satellite also sends a Home Assistant notification with a link to this guide.

To receive the notification, open **Settings → Devices & services → ESPHome**, select **Configure** for the satellite, and enable **Allow the device to perform Home Assistant actions**. The switch still refuses to turn on when credentials are missing even if notifications are disabled.
