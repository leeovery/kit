// kit.app: kit's own app, so its notifications come from kit, with its name
// and icon. Alerts (Info.plist), so they stay till dismissed; each posted
// under an id, which a later one replaces, and clear takes away. It quits
// once it's done: it never lingers.
//
//   notify --id <id> --title <title> --body <body> [--activate <bundle id>] [--at <ns>]
//   clear --id <id> [--at <ns>]
//   (no arguments)  launched by macOS for a click on a notification: brings
//                   forward the app the notification names, then quits
//
// --at is when kit sent it. kit sends them in order, but macOS can run two
// launched one after the other in either order, and the first notify holds
// while macOS asks the person to allow them: the newest sent under an id is
// kept, in kit's data folder, beside the app (kit empties it as a run
// starts), and one older does nothing, so a notification taken away before
// it could be posted never is, nor stays posted.

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
// for the id's hash, so any id fits a file's name.
let marks = Bundle.main.bundleURL.deletingLastPathComponent().appendingPathComponent("notices", isDirectory: true)

func markFile(_ id: String) -> URL {
    marks.appendingPathComponent(SHA256.hash(data: Data(id.utf8)).map { String(format: "%02x", $0) }.joined())
}

// Sent is a notify or clear as its mark keeps it: when kit sent it, and
// whether it was a clear.
struct Sent {
    var at: Int64
    var clear: Bool
}

// locked runs body holding a lock on the marks, so two of the app running
// at once never read and write them between each other.
func locked<T>(_ body: () -> T) -> T {
    try? FileManager.default.createDirectory(at: marks, withIntermediateDirectories: true)
    let fd = open(marks.appendingPathComponent(".lock").path, O_CREAT | O_RDWR, 0o644)
    if fd >= 0 {
        flock(fd, LOCK_EX)
    }
    defer {
        if fd >= 0 {
            flock(fd, LOCK_UN)
            close(fd)
        }
    }
    return body()
}

// newest is the newest notify or clear sent under id: at 0 for none.
func newest(_ id: String) -> Sent {
    guard let text = try? String(contentsOf: markFile(id), encoding: .utf8) else { return Sent(at: 0, clear: false) }
    let words = text.split(separator: " ")
    return Sent(at: words.first.flatMap { Int64($0) } ?? 0, clear: words.dropFirst().first == "clear")
}

// mark keeps sent as the newest under id, and reports whether it is: false
// when a newer one was, sent later. Marks never go back.
func mark(_ id: String, _ sent: Sent) -> Bool {
    locked {
        if newest(id).at > sent.at {
            return false
        }
        try? "\(sent.at) \(sent.clear ? "clear" : "notify")".write(to: markFile(id), atomically: true, encoding: .utf8)
        return true
    }
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
    let at = Int64(given["at"] ?? "") ?? 0
    if at > 0 && !mark(id, Sent(at: at, clear: false)) {
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
    // Taken away, or posted again, while macOS asked: nothing to post.
    if at > 0 && locked({ newest(id) }).at > at {
        return 0
    }
    let content = UNMutableNotificationContent()
    content.title = title
    content.body = given["body"] ?? ""
    content.sound = .default
    content.threadIdentifier = "kit"
    if let activate = given["activate"] {
        content.userInfo = ["activate": activate]
    }
    var failed = false
    wait(30) { finish in
        center.add(UNNotificationRequest(identifier: id, content: content, trigger: nil)) { error in
            failed = error != nil
            finish()
        }
    }
    // Taken away as it was posted: the clear, which marks before it
    // removes, may have found nothing yet.
    let last = locked { newest(id) }
    if at > 0 && last.clear && last.at > at {
        remove(id)
    }
    return failed ? 1 : 0
}

func clear(_ given: [String: String]) -> Int32 {
    guard let id = given["id"] else { return 64 }
    // One posted after it was sent stays.
    if let at = Int64(given["at"] ?? ""), !mark(id, Sent(at: at, clear: true)) {
        return 0
    }
    remove(id)
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
