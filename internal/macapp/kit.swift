// kit.app: kit's own app, so its notifications come from kit, with its name
// and icon. Alerts (Info.plist), so they stay till dismissed; each posted
// under an id, which a later one replaces, and clear takes away. It quits
// once it's done: it never lingers.
//
//   notify --id <id> --title <title> --body <body> [--activate <bundle id>]
//   clear --id <id>
//   (no arguments)  launched by macOS for a click on a notification: brings
//                   forward the app the notification names, then quits

import AppKit
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

// wait waits for a callback-style API to finish, for seconds at most.
func wait(_ seconds: Double, _ body: (@escaping () -> Void) -> Void) {
    let done = DispatchSemaphore(value: 0)
    body { done.signal() }
    _ = done.wait(timeout: .now() + seconds)
}

func post(_ given: [String: String]) -> Int32 {
    guard let id = given["id"], let title = given["title"] else { return 64 }
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
    wait(30) { finish in
        center.add(UNNotificationRequest(identifier: id, content: content, trigger: nil)) { error in
            failed = error != nil
            finish()
        }
    }
    return failed ? 1 : 0
}

func clear(_ given: [String: String]) -> Int32 {
    guard let id = given["id"] else { return 64 }
    let center = UNUserNotificationCenter.current()
    center.removeDeliveredNotifications(withIdentifiers: [id])
    center.removePendingNotificationRequests(withIdentifiers: [id])
    // Removing is asynchronous, and quitting at once can lose it.
    Thread.sleep(forTimeInterval: 1)
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
