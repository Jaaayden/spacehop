# Changelog

## 0.2.2

- Opening an app chosen from the Start menu is as fast as starting its exe: the shortcut file is read directly instead of being resolved by the shell (in the test VM, 380–480 ms down to 100–180 ms from key press to window)
- "Turn off the display" and "Sleep" wait until the keys are released, so letting go of the shortcut no longer wakes the screen at once
- Actions run strictly in the order they were triggered, and bringing a window to the front is verified and retried
- The desktop no longer counts as one of Explorer's windows when switching to it
- The activity log shows when Windows refused to bring a window forward

## 0.2.1

- The hint window no longer appears late the first time after start: it is drawn once out of sight shortly after launch, so fonts are already loaded

## 0.2.0

First release.

- Hold Space (or another leader key) and press keys to run actions; a tap still types
- Four kinds of leader key, side by side: hold with the quick tap kept, hold a dedicated key, tap a combination, tap a single modifier
- Twelve kinds of action: apps, URLs, files, commands, keystrokes, text, menu commands, window layouts, input methods, notifications, system
- Hint window with search; per-app scopes; prefix actions; repeatable actions
- Native self-drawn settings window with a keyboard map, in Chinese and English, dark and light
- Import and export, automatic backups, activity log, command-line control
- Single executable of about 4 MB for x64 and ARM64
