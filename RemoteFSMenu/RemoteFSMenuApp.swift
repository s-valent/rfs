import SwiftUI
import os.log

let logger = Logger(subsystem: "com.remotefs.menu", category: "AppDelegate")

@main
struct RemoteFSMenuApp: App {
    @NSApplicationDelegateAdaptor(AppDelegate.self) var appDelegate

    var body: some Scene {
        Settings {
            EmptyView()
        }
    }
}

class AppDelegate: NSObject, NSApplicationDelegate, MenuViewDelegate {
    var statusItem: NSStatusItem?
    var popover: NSPopover?
    var serverProcess: Process?
    var serverTimer: Timer?
    var currentConfig: ServerConfig?
    var healthStatus: HealthStatus = .stopped

    func applicationDidFinishLaunching(_ notification: Notification) {
        logger.info("App launched")
        
        statusItem = NSStatusBar.system.statusItem(withLength: NSStatusItem.variableLength)
        
        if let button = statusItem?.button {
            button.image = NSImage(systemSymbolName: "externaldrive.connected.to.line.below", accessibilityDescription: "Remote FS")
            button.action = #selector(togglePopover)
        }

        popover = NSPopover()
        popover?.contentSize = NSSize(width: 300, height: 400)
        popover?.behavior = .transient
        
        let menuView = MenuView(delegate: self)
        popover?.contentViewController = NSHostingController(rootView: menuView)
        logger.info("Popover initialized with delegate")
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
        logger.info("startServer called with config: \(config.sshAlias):\(config.remotePath)")
        
        stopServer()
        
        currentConfig = config
        healthStatus = .starting
        
        let goBinary = Bundle.main.bundlePath + "/Contents/MacOS/remote-fs"
        logger.info("Go binary path: \(goBinary)")
        
        // Check if binary exists
        let fileManager = FileManager.default
        if !fileManager.fileExists(atPath: goBinary) {
            logger.error("Binary does not exist at path: \(goBinary)")
            healthStatus = .error("Binary not found")
            return
        }
        
        let process = Process()
        process.executableURL = URL(fileURLWithPath: goBinary)
        var args = ["\(config.sshAlias):\(config.remotePath)"]
        if !config.mountPath.isEmpty {
            args.append(config.mountPath)
        }
        process.arguments = args
        
        logger.info("Full command: \(goBinary) \(config.sshAlias):\(config.remotePath) \"\(config.mountPath)\"")
        
        process.currentDirectoryURL = URL(fileURLWithPath: NSHomeDirectory())
        
        var env = ProcessInfo.processInfo.environment
        env["SSH_AUTH_SOCK"] = ProcessInfo.processInfo.environment["SSH_AUTH_SOCK"]
        process.environment = env
        
        let outputPipe = Pipe()
        let errorPipe = Pipe()
        process.standardOutput = outputPipe
        process.standardError = errorPipe
        
        let outputLog = FileManager.default.homeDirectoryForCurrentUser.appendingPathComponent("remote-fs.log")
        FileManager.default.createFile(atPath: outputLog.path, contents: nil)
        let logHandle = FileHandle(forWritingAtPath: outputLog.path)
        
        outputPipe.fileHandleForReading.readabilityHandler = { handle in
            let data = handle.availableData
            if let output = String(data: data, encoding: .utf8), !output.isEmpty {
                logger.info("Server output: \(output)")
                logHandle?.write(data)
            }
        }
        
        errorPipe.fileHandleForReading.readabilityHandler = { handle in
            let data = handle.availableData
            if let output = String(data: data, encoding: .utf8), !output.isEmpty {
                logger.error("Server error: \(output)")
                logHandle?.write(data)
            }
        }
        
        process.terminationHandler = { proc in
            let status = proc.terminationStatus
            logger.info("Process terminated with status: \(status)")
            DispatchQueue.main.async { [weak self] in
                if self?.serverProcess?.isRunning == true {
                    self?.healthStatus = .running
                } else {
                    self?.healthStatus = .error("Exit code: \(status)")
                }
            }
        }
        
        do {
            try process.run()
            serverProcess = process
            logger.info("Process started successfully")
        } catch {
            logger.error("Failed to start process: \(error.localizedDescription)")
            healthStatus = .error(error.localizedDescription)
        }
    }

    func stopServer() {
        serverProcess?.terminate()
        serverProcess = nil
        healthStatus = .stopped
        logger.info("Server stopped")
    }

    func checkHealth() {
        guard let process = serverProcess, process.isRunning else {
            healthStatus = .stopped
            return
        }
        healthStatus = .running
    }
}
