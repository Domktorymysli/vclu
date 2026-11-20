### Grenton

- **The Grenton app on iOS controls vclu objects** - the iOS app sends its commands with an empty address field (`req::<session>:...`). vclu dropped them as unknown, so switching from the app and its registration for reports did nothing, while the Android app worked. They are now run and answered like any other command.
