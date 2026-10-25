# Roadmap

[中文](roadmap.md) | English

- [X] Provide one-click Clash Verge Rev integration with global/rule mode and subscription following.
- [ ] Improve standalone Fudan campus access through local SOCKS5/HTTP proxies, sharing login and campus rules with Clash integration.
- [ ] Add a Home connection-card selector for “With Clash / Standalone,” defaulting to Clash; allow switching while disconnected, remember the choice, and show settings for the selected mode.
- [X] Improve upgrades and directory migration, handle conflicts with existing configurations, and support removing the FDU integration.
- [X] Add campus URL checks to show matching rules and access results through Clash.
- [X] Verify and improve connection recovery after outages, network changes, and waking from sleep.
- [X] Verify and improve FDU shutdown and integration recovery after unexpected Clash exits or controller failures.
- [X] Improve expired-session handling and provide a login action.
- [X] Support importing and exporting campus rules.
- [X] Verify and improve generic integration with FlClash and Clash Party on real installations.
- [X] Verify and improve generic integration with Sparkle, Clash Nyanpasu, and ClashX.Meta on real installations.
- [ ] Publish a compatibility list verified by client version and operating system.
- [X] Add one-click diagnostics for a user-entered campus URL, showing matching rules, proxy coverage, login and tunnel status, and DNS/TLS/HTTP results; distinguish expected routes from observed evidence and provide actionable suggestions.
- [ ] Improve recovery during startup, network changes, wake from sleep, and unexpected exits: wait for Clash to be ready before connecting automatically, recheck connections, and handle Clash restarts and unfinished recovery records; verify that ordinary Internet access still works after disabling FDU.
- [ ] Add skippable first-use guidance on Home that can be reopened, covering client confirmation, UIS login, and access verification while using the built-in campus rules.
- [ ] Improve tray status and quick actions with shortcuts to log in again and open diagnostics; notify only once when user action is required.
- [X] Support previewing, copying, and exporting redacted diagnostic reports with version, integration mode, rule status, and test results, hiding account identifiers, tokens, cookies, and personal intranet details.
