// kit.app: kit's own app, so its notifications come from kit, with its name
// and icon. Alerts (Info.plist), so they stay till dismissed; each posted
// under an id, which a later one replaces, and clear takes away. It quits
// once it's done: it never lingers.
//
//   notify --id <id> --title <title> --body <body> [--activate <bundle id>] [--at <ns> [--run <n>]]
//   clear --id <id> [--at <ns> [--run <n>]]
//   (no arguments)  launched by macOS for a click on a notification: brings
//                   forward the app the notification names, then quits
//
// --at is when kit sent it, --run which run of kit's did. kit sends them in
// order, but macOS can run two launched one after the other in either
// order, and the first notify holds while macOS asks the person to allow
// them: the newest sent under an id is kept, in kit's data folder, beside
// the app, ordered by run, then time, and one older does nothing, so a
// notification taken away before it could be posted never is, nor stays
// posted; nor is one posted that a run sent once a later run has begun.

import AppKit
import CryptoKit
import UserNotifications

let args = Array(CommandLine.arguments.dropFirst().filter { !$0.hasPrefix("-psn_") })

// options reads --name value pairs: a value is taken as it is, even one
// starting with --.
func options(_ words: ArraySlice<String>) -> [String: String] {
    var found: [String: String] = [:]
    var i = words.startIndex
    while i < words.endIndex {
        if words[i].hasPrefix("--"), i + 1 < words.endIndex {
            found[String(words[i].dropFirst(2))] = words[i + 1]
            i += 2
        } else {
            i += 1
        }
    }
    return found
}

// marks is where the newest sent under each id is kept, a file each, named
// for the id's hash, so any id fits a file's name; and, in runs, the newest
// run of kit's to send any. lock is the lock kit and the app take to read
// and write them, beside the folder, never in it.
let data = Bundle.main.bundleURL.deletingLastPathComponent()
let marks = data.appendingPathComponent("notices", isDirectory: true)
let runs = marks.appendingPathComponent("run")
let lock = data.appendingPathComponent("notices.lock")

func markFile(_ id: String) -> URL {
    marks.appendingPathComponent(SHA256.hash(data: Data(id.utf8)).map { String(format: "%02x", $0) }.joined())
}

// Sent is a notify or clear as kit sent it and its mark keeps it: the run
// that sent it, when, and whether it was a clear. A mark from before runs
// were numbered has run 0.
struct Sent: Equatable {
    var run: Int64
    var at: Int64
    var clear: Bool

    // isNewer reports whether it was sent after other: by a later run, or
    // later in the same run.
    func isNewer(than other: Sent) -> Bool {
        run != other.run ? run > other.run : at > other.at
    }
}

// sent is what kit says of the notify or clear it sent: nil when it says
// nothing of when, as by hand, so that it's done whatever was sent before.
// One that says no run is taken as sent before runs were numbered.
func sent(_ given: [String: String], clear: Bool) -> Sent? {
    guard let at = Int64(given["at"] ?? "") else { return nil }
    return Sent(run: Int64(given["run"] ?? "") ?? 0, at: at, clear: clear)
}

// locked runs body holding the lock on the marks, so that no two of kit
// and the app read and write them between each other: nil, body not run,
// when the lock can't be had.
func locked<T>(_ body: () -> T) -> T? {
    let fd = open(lock.path, O_CREAT | O_RDWR, 0o644)
    guard fd >= 0 else { return nil }
    defer { close(fd) }
    guard flock(fd, LOCK_EX) == 0 else { return nil }
    defer { flock(fd, LOCK_UN) }
    return body()
}

// newestRun is the newest run of kit's to send notifications: 0 for none.
// The lock is held.
func newestRun() -> Int64 {
    guard let text = try? String(contentsOf: runs, encoding: .utf8) else { return 0 }
    return Int64(text.trimmingCharacters(in: .whitespacesAndNewlines)) ?? 0
}

// newest is the newest notify or clear sent under id: run and at 0 for
// none. The lock is held.
func newest(_ id: String) -> Sent {
    guard let text = try? String(contentsOf: markFile(id), encoding: .utf8) else { return Sent(run: 0, at: 0, clear: false) }
    let words = text.split(separator: " ").map(String.init)
    let numbers = words.dropLast().compactMap { Int64($0) }
    let clear = words.last == "clear"
    switch numbers.count {
    case 2: return Sent(run: numbers[0], at: numbers[1], clear: clear)
    case 1: return Sent(run: 0, at: numbers[0], clear: clear)
    default: return Sent(run: 0, at: 0, clear: false)
    }
}

// mark keeps sent as the newest under id, reporting whether it did: false
// when one sent after it is, or it couldn't be written. The lock is held.
func mark(_ id: String, _ sent: Sent) -> Bool {
    if newest(id).isNewer(than: sent) {
        return false
    }
    do {
        try FileManager.default.createDirectory(at: marks, withIntermediateDirectories: true)
        try "\(sent.run) \(sent.at) \(sent.clear ? "clear" : "notify")".write(to: markFile(id), atomically: true, encoding: .utf8)
    } catch {
        return false
    }
    return true
}

// remove takes away what's posted, or waits to be, under id.
func remove(_ id: String) {
    let center = UNUserNotificationCenter.current()
    center.removeDeliveredNotifications(withIdentifiers: [id])
    center.removePendingNotificationRequests(withIdentifiers: [id])
    // Removing is asynchronous, and quitting at once can lose it.
    Thread.sleep(forTimeInterval: 1)
}

// wait waits for a callback-style API to finish, for seconds at most.
func wait(_ seconds: Double, _ body: (@escaping () -> Void) -> Void) {
    let done = DispatchSemaphore(value: 0)
    body { done.signal() }
    _ = done.wait(timeout: .now() + seconds)
}

func post(_ given: [String: String]) -> Int32 {
    guard let id = given["id"], let title = given["title"] else { return 64 }
    let mine = sent(given, clear: false)
    // What a run sent once a later one's begun, or sent before the newest
    // under its id, isn't posted.
    if let mine, locked({ mine.run >= newestRun() && mark(id, mine) }) != true {
        return 0
    }
    let center = UNUserNotificationCenter.current()
    var allowed = false
    // The first time, macOS asks the person: give them a while to answer.
    wait(120) { finish in
        center.requestAuthorization(options: [.alert, .sound]) { granted, _ in
            allowed = granted
            finish()
        }
    }
    guard allowed else { return 1 }
    let content = UNMutableNotificationContent()
    content.title = title
    content.body = given["body"] ?? ""
    content.sound = .default
    content.threadIdentifier = "kit"
    if let activate = given["activate"] {
        content.userInfo = ["activate": activate]
    }
    var failed = false
    // Posted only while it's still the newest sent under its id, by the
    // newest run, checked and added under the lock, as a clear marks and
    // removes under it: taken away, sent again, or a later run begun while
    // macOS asked, it isn't.
    let added = locked { () -> Bool in
        if let mine, mine.run < newestRun() || newest(id) != mine {
            return false
        }
        wait(30) { finish in
            center.add(UNNotificationRequest(identifier: id, content: content, trigger: nil)) { error in
                failed = error != nil
                finish()
            }
        }
        return true
    }
    guard added == true else { return 0 }
    // macOS can deliver it after a clear sent just after has removed what
    // was there.
    if let mine, let last = locked({ newest(id) }), last.clear, last.isNewer(than: mine) {
        remove(id)
    }
    return failed ? 1 : 0
}

func clear(_ given: [String: String]) -> Int32 {
    guard let id = given["id"] else { return 64 }
    // Marked and removed under the lock, so that no notify adds between:
    // one sent after it stays.
    _ = locked {
        if let mine = sent(given, clear: true), !mark(id, mine) {
            return
        }
        remove(id)
    }
    return 0
}

func activate(_ bundle: String) {
    guard let url = NSWorkspace.shared.urlForApplication(withBundleIdentifier: bundle) else { return }
    let configuration = NSWorkspace.OpenConfiguration()
    configuration.activates = true
    wait(5) { finish in
        NSWorkspace.shared.openApplication(at: url, configuration: configuration) { _, _ in finish() }
    }
}

final class Delegate: NSObject, NSApplicationDelegate, UNUserNotificationCenterDelegate {
    func applicationDidFinishLaunching(_ notification: Notification) {
        UNUserNotificationCenter.current().delegate = self
        guard let command = args.first else {
            // A click's response comes to the delegate below: quit anyway
            // should none come, as when opened by hand.
            DispatchQueue.main.asyncAfter(deadline: .now() + 5) { exit(0) }
            return
        }
        DispatchQueue.global().async {
            let given = options(args.dropFirst())
            switch command {
            case "notify": exit(post(given))
            case "clear": exit(clear(given))
            default: exit(64)
            }
        }
    }

    func userNotificationCenter(_ center: UNUserNotificationCenter, didReceive response: UNNotificationResponse,
                                withCompletionHandler completionHandler: @escaping () -> Void) {
        if let bundle = response.notification.request.content.userInfo["activate"] as? String {
            activate(bundle)
        }
        completionHandler()
        if args.isEmpty {
            exit(0)
        }
    }

    func userNotificationCenter(_ center: UNUserNotificationCenter, willPresent notification: UNNotification,
                                withCompletionHandler completionHandler: @escaping (UNNotificationPresentationOptions) -> Void) {
        completionHandler([.banner, .list, .sound])
    }
}

let app = NSApplication.shared
let delegate = Delegate()
app.delegate = delegate
app.run()
