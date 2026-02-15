import SwiftUI
import os.log

let logger = Logger(subsystem: "com.remotefs.app", category: "AppDelegate")

@main
struct RemoteFSMenuApp: App {
    @NSApplicationDelegateAdaptor(AppDelegate.self) var appDelegate

    var body: some Scene {
        Settings {
            EmptyView()
        }
    }
}

class ServerStore: ObservableObject {
    @Published var servers: [UUID: ServerInstance] = [:]
}

class ServerInstance: Identifiable, ObservableObject {
    let id: UUID
    let config: ServerConfig
    var process: Process?
    @Published var status: HealthStatus = .stopped
    
    init(config: ServerConfig) {
        self.id = config.id
        self.config = config
    }
}

class AppDelegate: NSObject, NSApplicationDelegate, MenuViewDelegate {
    var statusItem: NSStatusItem?
    var popover: NSPopover?
    var serverStore = ServerStore()

    func applicationDidFinishLaunching(_ notification: Notification) {
        statusItem = NSStatusBar.system.statusItem(withLength: NSStatusItem.variableLength)
        
        if let button = statusItem?.button {
            button.image = NSImage(systemSymbolName: "externaldrive.connected.to.line.below", accessibilityDescription: "Remote FS")
            button.action = #selector(togglePopover)
        }

        popover = NSPopover()
        popover?.contentSize = NSSize(width: 320, height: 400)
        popover?.behavior = .transient
        
        let menuView = MenuView(delegate: self, serverStore: serverStore)
        popover?.contentViewController = NSHostingController(rootView: menuView)
    }

    @objc func togglePopover() {
        guard let button = statusItem?.button else { return }
        if let popover = popover {
            if popover.isShown {
                popover.performClose(nil)
            } else {
                popover.show(relativeTo: button.bounds, of: button, preferredEdge: .minY)
            }
        }
    }

    func startServer(config: ServerConfig) {
        if serverStore.servers[config.id] != nil {
            return
        }
        
        let goBinary = Bundle.main.bundlePath + "/Contents/MacOS/remote-fs"
        
        let fileManager = FileManager.default
        if !fileManager.fileExists(atPath: goBinary) {
            let instance = ServerInstance(config: config)
            instance.status = .error("Binary not found")
            serverStore.servers[config.id] = instance
            return
        }
        
        let process = Process()
        process.executableURL = URL(fileURLWithPath: goBinary)
        var args = ["\(config.sshAlias):\(config.remotePath)"]
        if !config.mountPath.isEmpty {
            args.append(config.mountPath)
        }
        process.arguments = args
        
        process.currentDirectoryURL = URL(fileURLWithPath: NSHomeDirectory())
        
        let outputPipe = Pipe()
        let errorPipe = Pipe()
        process.standardOutput = outputPipe
        process.standardError = errorPipe
        
        let instance = ServerInstance(config: config)
        instance.status = .starting
        instance.process = process
        
        process.terminationHandler = { [weak self] proc in
            let status = proc.terminationStatus
            DispatchQueue.main.async {
                if status == 0 {
                    self?.serverStore.servers[config.id]?.status = .stopped
                } else {
                    self?.serverStore.servers[config.id]?.status = .error("Exit code: \(status)")
                }
                self?.serverStore.objectWillChange.send()
            }
        }
        
        do {
            try process.run()
            serverStore.servers[config.id] = instance
        } catch {
            let errInstance = ServerInstance(config: config)
            errInstance.status = .error(error.localizedDescription)
            serverStore.servers[config.id] = errInstance
        }
    }

    func stopServer(config: ServerConfig) {
        if let instance = serverStore.servers[config.id] {
            instance.process?.terminate()
            serverStore.servers[config.id] = nil
        }
    }

    func checkHealth() {
        var changed = false
        for (id, instance) in serverStore.servers {
            if let process = instance.process, process.isRunning {
                if case .running = instance.status {
                } else {
                    instance.status = .running
                    changed = true
                }
            } else if case .starting = instance.status {
            } else if case .error = instance.status {
            } else {
                if case .stopped = instance.status {
                } else {
                    instance.status = .stopped
                    changed = true
                }
            }
            serverStore.servers[id] = instance
        }
        if changed {
            serverStore.objectWillChange.send()
        }
    }
    
    func getStatus(for configId: UUID) -> HealthStatus {
        return serverStore.servers[configId]?.status ?? .stopped
    }
}
