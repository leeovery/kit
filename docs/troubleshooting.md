# Troubleshooting: what kit's messages mean, and the fix

Each entry starts from what kit says, then gives the cause and the fix. Most messages name the
command that puts things right, so read the whole line first.

## "no config repository"

```console
kit: no config repository: no kit.toml in /Users/someone/.config/kit: set this Mac up with kit setup, clone your config repository to /Users/someone/.config/kit, or point KIT_CONFIG at it
```

**Cause:** kit found no `kit.toml` in the folder it reads kit-config from, so it stops.

**Fix:** if kit doesn't look after this Mac yet, run `kit setup`. If it does, clone kit-config
into the folder the message names:

```bash
# Clone kit-config where kit looks for it
gh repo clone owner/kit-config ~/.config/kit
```

`KIT_CONFIG` can point kit at a clone somewhere else, but it reaches only the shell that sets
it. The hourly run still reads the folder the message names, so prefer the clone.
[Configuration](configuration.md) says where kit looks.

## kit-config is public

The boot stops with:

```text
owner/kit-config is public on GitHub, and a config repository is always private: make it private, then boot again
```

On a Mac in use, `kit status` says so under CONFIG:

```text
public on GitHub (owner/kit-config): it must be private
```

`kit status` also shows the command that makes it private:

<img src="../art/troubleshooting-private.webp" alt="kit status under CONFIG: kit-config public on GitHub, with the command that makes it private" width="573">

**Cause:** kit-config names your Macs, apps and where your secrets are kept, so kit insists it
stays private.

**Fix:** make it private on GitHub, then boot again or run `kit status`:

```bash
gh repo edit owner/kit-config \
  --visibility private \
  --accept-visibility-change-consequences
```

If `kit status` says it `can't prove` kit-config private, the remote isn't a github.com URL
kit reads. The message lists the forms kit reads.

## An install waits for a password

```text
waiting for an administrator's password: kit apply at a terminal asks for it
needs an administrator's password: run kit apply at a terminal
```

**Cause:** some installs need an administrator's password:

- a cask that installs through a package;
- an App Store app;
- some macOS and power settings;
- backup and Spotlight exclusions;
- a step declared `--sudo`.

kit asks for the password only at a terminal. The hourly run, an agent and a run with `--json`
or `--plain` have nobody to ask, so those items wait. The rest of the run carries on.

**Fix:** run `kit apply` at a terminal. kit asks for the password once, as the run starts. To
apply one step, name it, such as `kit apply fonts`.

## 1Password's approval timed out

The secrets step says `1Password wasn't approved, or couldn't read them`. Then it gives what
1Password said, and how to have it answer.

**Cause:** 1Password asks you to approve a read of your secrets once its last approval has
lapsed. kit waits up to 10 minutes for that answer. If nobody approves in time, kit holds back the secrets it hasn't
read until the next sync. Whatever waits on them waits too.

**Fix:**

1. Unlock the 1Password app.
2. Turn on Settings › Developer › Integrate with 1Password CLI.
3. Run `kit secret sync`, and approve the prompt.

If kit says 1Password isn't signed in, and both are done, quit 1Password and open it again.
Over a remote connection, sign in with a password in the shell kit runs from, as the message
says.

## App Store apps don't install

```text
couldn't install bear@1091189122, numbers@409203825, xcode@497799835: no one's signed in: sign in to the App Store, then try again
```

The message may say `the App Store didn't answer` instead.

**Cause:** an App Store install needs someone signed in to the App Store. kit can't sign you in.

**Fix:** open the App Store app, sign in, then run `kit apply mas`.

On a new Mac, declare the sign-in as a [step by hand](steps.md) that comes before the App Store
apps. Write `--before mas` on its line in `[manual]`:

```text
[manual]
app-store "Sign in to the App Store" --before mas
```

kit then asks for the sign-in partway through the run. If an install still finds nobody signed
in, kit asks again on the next run.

## A cask fails because its app is already there

kit has Homebrew take over an app already in Applications only if it's the cask's own, at a
version the cask takes over. If it isn't, the cask fails, and the app stays as it is. The
version depends on the cask:

| If the cask | It takes over |
|---|---|
| Updates itself, no package | Its major version or later, as it is |
| Doesn't, or has a package | The same version, or an older copy of its major |
| Is a channel, as `@beta` | A prerelease of its major |

A prerelease app, such as a beta, goes only to a channel's cask if one is there.

The reason comes after the start of the message:

| Command | Message starts |
|---|---|
| `kit apply` | `couldn't install <cask>:` |
| `kit cask add` | `couldn't install:` |
| `kit reconcile` | `couldn't install:` |

With four casks or more failing, `kit apply` starts `couldn't install 4 of 6:` instead. Two
reasons or more are joined with `;`. The fix for each is the same: choose which copy you
keep.

### The app isn't the cask's

```text
Spaceman.app is already in /Applications and isn't the cask's (com.example.Spaceman)
```

**Cause:** the cask doesn't name the bundle id of the app there. A fork with the same name
does this. If the message ends `(no bundle id)`, the app names no bundle id at all.

### The App Store's copy is there

```text
Slack.app is already in /Applications and is the App Store's
```

**Cause:** the app came from the App Store. kit never hands the App Store's copy to Homebrew,
even if the cask names its bundle id.

### Another version is there

```text
Charles.app is 4.6.8 and the cask's is 5.2.3: kit replaces only an older copy of the same major version
```

**Cause:** the cask doesn't update itself, or installs through a package. kit replaces the
app only if it's the same version or an older copy of the same major version. These stay as
they are:

- a newer copy, and the message adds `this one is newer`;
- an earlier major version, such as Charles 4 beside the cask's 5;
- a prerelease, or an app that says no version.

If you want to keep an earlier major version, Homebrew may have a cask for it, such as
`charles@4`. Declare that one instead.

### An earlier major version is there

```text
BBEdit.app is 14.6.2 and the cask's is 16.1: kit takes over only a copy of the cask's major version or later, and bbedit@14 is this one's line
```

**Cause:** the cask updates itself, so it takes over a copy of its major version or later,
a newer one too. An earlier major version may be one you have no licence past, so kit leaves
it. The message names the cask for your version, if Homebrew has one.

If it ends `can't tell whether this one is`, kit can't compare the two versions. That happens
in these cases:

- the app says no version kit can read;
- the cask's version is `latest`, and a line of the cask is there too;
- the app's version is a build number below the cask's, such as Sublime Text's 4200 beside
  4215.

A build number below the cask's fails `kit apply` for a declared cask until the app updates
itself past it. Run `kit apply cask` again then.

**Fix:** to keep your version, declare its line in place of the cask:

```bash
kit reconcile cask:bbedit --undeclare
kit cask add bbedit@14
```

Or undeclare the cask, and keep the app by hand.

### A prerelease is there

```text
Thaw.app is 3.0.0-beta.7 and the cask's is 2.1.0: kit takes over a prerelease only by its channel's cask, and thaw@beta is this one's
```

**Cause:** the app is a prerelease, and Homebrew has a channel's cask for it, such as a beta.
kit hands a prerelease only to that cask. If the message ends `none here is at its major
version`, no channel matches the app's major version.

**Fix:** declare the channel's cask in place of this one, as for a line above:
`kit cask add thaw@beta`. Or undeclare the cask, and keep the app by hand.

### kit can't look at the app

```text
can't tell whose Spaceman.app in /Applications is: …
```

**Cause:** kit couldn't read the app, for the reason after the last colon. Put that right,
then run `kit apply cask`.

### Homebrew says there's already an App

The message holds Homebrew's own words, `It seems there is already an App at`.

**Cause:** kit couldn't check the cask, so Homebrew installed it without taking the app over.
That happens for these casks:

- a cask from a tap;
- a cask that puts more than apps in place, such as a font;
- a cask missing from Homebrew's catalogue;
- every cask, if the catalogue can't be read.

### The fix: choose which copy you keep

| To keep | Do this |
|---|---|
| The cask's | Move the app to the Bin, then `kit apply cask` |
| Yours | `kit reconcile cask:<name> --undeclare` |

If `[app store apps]` declares the app in the way, moving it to the Bin isn't enough:
`kit apply` installs it again. Run `kit mas remove <app>` instead, which uninstalls it and takes
its line out.

If you keep yours, the app stays as it is. If the cask is in the shared file, add `--shared`.
That takes the cask off every Mac: no answer keeps a shared cask off one Mac only.

## kit.app needs Full Disk Access again

`kit status` shows the step `kit-app-access` as `not given to this build of kit.app`.

**Cause:** macOS sees another kit.app than the one you gave access to. Releases ship kit.app
signed, so updates keep the grant. Two things change kit.app all the same:

- the first update that ships kit.app replaces the kit.app your Mac built;
- a kit you built yourself builds kit.app from changed files.

macOS treats the new kit.app as another app, and the old grant doesn't carry over.

**Fix:** run `kit apply` at a terminal, with no step named. It opens System Settings at Full
Disk Access, and Finder at kit.app. Then:

1. Remove the old kit.app from the list, with −.
2. Drag the new kit.app into the list, and switch it on.
3. Say so on kit's card.

The next hourly run checks the grant.

## Backup exclusions wait for Full Disk Access

The Mac settings row of `kit apply` says `needs Full Disk Access`. Its To do lists "Give
Ghostty Full Disk Access, then reopen it", with your terminal's name.

**Cause:** `tmutil` changes Time Machine's list only for an app with Full Disk Access. The
terminal kit runs in doesn't have it, and a terminal takes up the access only once it's
reopened.

**Fix:** follow the to-do's steps:

1. In System Settings › Privacy & Security › Full Disk Access, turn on your terminal.
2. Quit the terminal and open it again.
3. Run `kit apply backup-exclusion`.

Over SSH, the to-do is "Allow Full Disk Access for remote users" instead. You find that switch
in System Settings › General › Sharing › Remote Login › ⓘ.

## A linked file differs

`kit apply` leaves the file as it is, and says so under Files and shell in its record:

<img src="../art/troubleshooting-differs.webp" alt="kit apply's record: Files and shell done, with ~/.zshrc a file of its own, different from kit-config's" width="488">

**Cause:** something replaced the link with a file of its own. An app that saves by writing a
new file does this. kit never overwrites it, because the two differ.

**Fix:** keep one copy with `kit reconcile`:

```bash
# Keep this Mac's copy: it goes into kit-config
kit reconcile file:~/.zshrc --adopt
# Keep kit-config's: this Mac's goes to the Bin
kit reconcile file:~/.zshrc --revert
```

Either way, kit links the file again. At a terminal, `kit reconcile` asks about each one.
[Linked files](linked-files.md) says more.

## Your other Mac's changes don't come in

`kit status` shows a row under CONFIG that starts with this, then says why:

```text
the remote's commits not brought in
```

**Cause:** each run pulls kit-config before it reads it, and this pull couldn't happen. kit
pulls only if it can leave your edits and commits as they are. Being offline stops the pull
too. The row stays grey for a day, then needs you.

**Fix:** if you're offline, do nothing: the next run tries again. Otherwise, do what the row
says. Each reason ends with what to do:

| The row says | What to do |
|---|---|
| change what's not committed | See the next section |
| conflict with this Mac's | See the next section |
| which git ignores | Move that file aside |
| set aside what's not committed | Commit or undo the edit |
| pulling would delete | Commit or undo the change |
| is under way | Finish it, or abort it |
| is waiting to be resolved | Resolve the conflict |
| git signs its commits | Pull and push by hand |
| history was rewritten | See the last section |

To pull and push by hand, run `git -C ~/.config/kit pull --rebase`, then
`git -C ~/.config/kit push`. git signs each commit it rebases as it goes.

### You and the other Mac changed one file

To keep the other Mac's change, undo yours:

```bash
# Put the file back as last committed
kit reconcile config:laptop/declarations --revert
```

To keep both changes, commit yours, then bring theirs in by hand. If the row says "conflict
with this Mac's", yours is committed already: start at the pull. The pull stops on the conflict
and leaves git's markers in the file:

```bash
# Commit your edit
kit reconcile config:laptop/declarations --adopt
# Bring theirs in: it stops on the conflict
git -C ~/.config/kit pull --rebase --autostash
```

Edit the file to keep what you want from both, and remove the markers. Then finish. If
`rebase --continue` opens your editor with the commit message, save and close it:

```bash
# Mark the file resolved, then carry on
git -C ~/.config/kit add laptop/declarations
git -C ~/.config/kit rebase --continue
```

Then push the result:

```bash
git -C ~/.config/kit push
```

### Another Mac rewrote kit-config's history

Another Mac force-pushed kit-config, dropping a commit this Mac still has. kit won't put that
commit back. First, list the commits here that the remote doesn't have, newest first:

```bash
git -C ~/.config/kit log --oneline @{u}..
```

The list holds the commits the other Mac dropped, and any of yours not pushed yet. If none of
them is yours, match the remote. Your edits that aren't committed stay:

```bash
git -C ~/.config/kit reset --keep @{u}
```

If that says `not uptodate`, an edit touches a dropped file: commit or undo the edit, then run
it again.

If some of them are yours, keep those and leave the dropped ones out. Name the newest dropped
commit from the list, here `1a2b3c4`. Your commits above it in the list stay:

```bash
git -C ~/.config/kit rebase --autostash \
  --onto @{u} 1a2b3c4
git -C ~/.config/kit push
```

## Related

- [Commands](commands.md)
- [Configuration](configuration.md)
- [Drift: keep or undo what changed by hand](drift.md)
