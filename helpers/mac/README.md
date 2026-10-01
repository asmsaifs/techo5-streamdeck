# deckcap-mac

macOS capture helper, protocol in [docs/helpers.md](../../docs/helpers.md).

    cd helpers/mac && swift build -c release     # .build/release/deckcap-mac

Needs **Screen Recording** (list and capture) and **Accessibility** (mouse input) for the process
that starts it. Run bare from Terminal the grants go to Terminal; shipped inside the app bundle
(Phase 8) they go to the app. Without a grant it sends an `error` event with code `permission`.
