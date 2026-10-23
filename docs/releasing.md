# Releasing KnockBlock

Every merge into `main` is released automatically. The `Release` workflow
(`.github/workflows/release.yml`):

1. picks the version: the latest `vX.Y.Z` tag plus one patch (`v0.1.0` for
   the first release), so merges give `v0.1.1`, `v0.1.2`, …;
2. builds the firmware bundle (with that version as the device's project
   version) and the MSI;
3. installs the MSI on a Windows runner, starts the app, checks the idle
   memory against the 20 MB ceiling (CLAUDE.md §2.1), then uninstalls it;
4. creates the tag and publishes both files as a GitHub release. The text
   comes from [`release-notes.md`](release-notes.md), followed by the notes
   GitHub generates from the merged PRs.

If a step fails, nothing is published and no tag is created; the next merge
(or a re-run of the workflow) releases instead. Merges released while one
run is still going wait for it, so versions never collide.

To start a new **minor or major** series (for example after a feature set
you have tested on the hardware), push the tag by hand; later merges then
continue from it (`v0.2.0` → `v0.2.1` …):

```sh
git checkout main && git pull
git tag -a v0.2.0 -m "KnockBlock 0.2.0"
git push origin v0.2.0
```

Versions follow [Semantic Versioning](https://semver.org/). The MSI needs a
purely numeric version, so pre-release tags such as `v0.1.0-rc1` are not
used. A newer MSI installs over an older one.

## Checklist

### Before a minor or major release

Patch releases need no checklist. Before tagging a new minor or major
version:

- [ ] CI on `main` is green: the `CI` and `Release` workflows.
- [ ] `CHANGELOG.md` has a section for the new version, dated, with the
      user-visible changes since the previous one.
- [ ] `proto/VERSION` matches the ESPHome version you tested with. Change it
      only on purpose: it pins both the protobuf code and the ESPHome
      version in CI.
- [ ] The MSI and the firmware bundle of the latest automatic release pass
      the hardware checks below.

### Hardware checks (by a person; CI cannot do these)

Firmware, on the Waveshare ESP32-C6-LCD-1.47:

- [ ] Flash the bundle: `esphome run knockblock.yaml`.
- [ ] The display shows the status label. Switching `display_language`
      between `en` and `it` changes it, including accented letters.
- [ ] The LED colours are right: green free, amber mic, orange camera, red
      call, purple do not disturb, LED off when offline.
- [ ] The detail line shows "app · PC". `privacy_mode` hides it.
- [ ] Unplug the PC from the network: after about 2 minutes the light goes
      to `offline`.

Windows app:

- [ ] Install the MSI. On first start the settings window opens; it looks
      right at 100 % and 150 % display scaling, and Tab, Enter (Save) and Esc
      (Cancel) work.
- [ ] Test connection works with the right key, and reports a clear error
      with a wrong key or address.
- [ ] Save. The tray icon turns green and the light shows "Free".
- [ ] A Teams, Discord or Zoom call makes the light show `call` with the app
      name. Muting the camera gives `mic`.
- [ ] Do not disturb and Free from the tray menu override the status.
      Automatic goes back to detection.
- [ ] Sleep and wake the PC: the light catches up within seconds of waking.
- [ ] Task Manager shows the app under 15 MB in the "Memory" column.
- [ ] Quit: the light shows `offline` for this PC (or another PC's status).
- [ ] Uninstall from Settings → Apps: the app, its shortcut and its
      autostart entry are gone.

Write down anything new about detection in
[`detection-notes.md`](detection-notes.md).

### Tagging

Push the tag as shown above and follow the `Release` run in the Actions
tab. If a job fails, fix the problem on `main` (that merge is released as a
patch of the previous series), then delete the tag locally and on GitHub
and push it again on the new commit.

### After publishing

- [ ] The release has `KnockBlock-X.Y.Z.msi` and
      `knockblock-X.Y.Z.esphomebundle.tar.gz`.
- [ ] Download the MSI from the release page and install it once more. The
      Windows properties of `knockblock.exe` show the new version.
- [ ] Remote-package users can pin the release with `knockblock_ref: vX.Y.Z`
      (see `firmware/examples/remote-package.yaml`). The examples stay on
      `main`.
