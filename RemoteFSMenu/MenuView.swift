import SwiftUI
import os.log

let menuLogger = Logger(subsystem: "com.remotefs.menu", category: "MenuView")

enum HealthStatus {
    case stopped
    case starting
    case running
    case error(String)
}

protocol MenuViewDelegate: AnyObject {
    func startServer(config: ServerConfig)
    func stopServer()
    func checkHealth()
    var healthStatus: HealthStatus { get }
    var currentConfig: ServerConfig? { get }
}

struct MenuView: View {
    weak var delegate: MenuViewDelegate?
    @StateObject private var store = ServerConfigStore()
    @State private var isAdding = false
    @State private var editingConfig: ServerConfig?
    
    var body: some View {
        VStack(spacing: 0) {
            headerView
            
            Divider()
            
            if store.configs.isEmpty {
                emptyStateView
            } else {
                configsList
            }
            
            Divider()
            
            footerView
        }
        .frame(width: 300, height: 400)
        .sheet(isPresented: $isAdding) {
            ConfigEditorView(config: ServerConfig()) { newConfig in
                store.add(newConfig)
                isAdding = false
            }
        }
        .sheet(item: $editingConfig) { config in
            ConfigEditorView(config: config) { updatedConfig in
                store.update(updatedConfig)
                editingConfig = nil
            }
        }
    }
    
    private var headerView: some View {
        HStack {
            Text("Remote FS")
                .font(.headline)
            Spacer()
            statusIndicator
        }
        .padding()
    }
    
    private var statusIndicator: some View {
        Group {
            switch delegate?.healthStatus ?? .stopped {
            case .stopped:
                Circle()
                    .fill(Color.gray)
                    .frame(width: 10, height: 10)
            case .starting:
                Circle()
                    .fill(Color.orange)
                    .frame(width: 10, height: 10)
            case .running:
                Circle()
                    .fill(Color.green)
                    .frame(width: 10, height: 10)
            case .error(let msg):
                Circle()
                    .fill(Color.red)
                    .frame(width: 10, height: 10)
                Text(msg)
                    .font(.caption)
                    .foregroundColor(.red)
            }
        }
    }
    
    private var emptyStateView: some View {
        VStack(spacing: 12) {
            Spacer()
            Image(systemName: "externaldrive.badge.questionmark")
                .font(.system(size: 40))
                .foregroundColor(.secondary)
            Text("No connections configured")
                .foregroundColor(.secondary)
            Button("Add Connection") {
                isAdding = true
            }
            Spacer()
        }
    }
    
    private var configsList: some View {
        List {
            ForEach(store.configs) { config in
                ConfigRowView(
                    config: config,
                    isActive: delegate?.currentConfig?.id == config.id && {
                        if case .running = delegate?.healthStatus {
                            return true
                        }
                        return false
                    }(),
                    onStart: {
                        menuLogger.info("Start button pressed for config: \(config.sshAlias)")
                        delegate?.startServer(config: config)
                    },
                    onStop: {
                        delegate?.stopServer()
                    },
                    onEdit: {
                        editingConfig = config
                    },
                    onDelete: {
                        if let idx = store.configs.firstIndex(where: { $0.id == config.id }) {
                            store.remove(at: idx)
                        }
                    }
                )
            }
        }
        .listStyle(.plain)
    }
    
    private var footerView: some View {
        HStack {
            Button(action: { isAdding = true }) {
                Image(systemName: "plus")
            }
            .buttonStyle(.borderless)
            
            Spacer()
            
            if let status = delegate?.healthStatus, case .running = status {
                Button("Stop") {
                    delegate?.stopServer()
                }
                .buttonStyle(.bordered)
                .tint(.red)
            }
            
            Button("Quit") {
                NSApplication.shared.terminate(nil)
            }
            .buttonStyle(.borderless)
        }
        .padding()
    }
}

struct ConfigRowView: View {
    let config: ServerConfig
    let isActive: Bool
    let onStart: () -> Void
    let onStop: () -> Void
    let onEdit: () -> Void
    let onDelete: () -> Void
    
    var body: some View {
        HStack {
            VStack(alignment: .leading, spacing: 2) {
                Text(config.sshAlias)
                    .font(.headline)
                Text(config.remotePath)
                    .font(.caption)
                    .foregroundColor(.secondary)
            }
            
            Spacer()
            
            if isActive {
                Text("Active")
                    .font(.caption)
                    .foregroundColor(.green)
                    .padding(.horizontal, 6)
                    .padding(.vertical, 2)
                    .background(Color.green.opacity(0.2))
                    .cornerRadius(4)
            }
            
            if isActive {
                Button(action: onStop) {
                    Image(systemName: "stop.fill")
                        .foregroundColor(.red)
                }
                .buttonStyle(.borderless)
            } else {
                Button(action: onStart) {
                    Image(systemName: "play.fill")
                        .foregroundColor(.green)
                }
                .buttonStyle(.borderless)
            }
            
            Button(action: onEdit) {
                Image(systemName: "pencil")
            }
            .buttonStyle(.borderless)
            
            Button(action: onDelete) {
                Image(systemName: "trash")
                    .foregroundColor(.red)
            }
            .buttonStyle(.borderless)
        }
        .padding(.vertical, 4)
    }
}

struct ConfigEditorView: View {
    @Environment(\.dismiss) var dismiss
    @State var config: ServerConfig
    let onSave: (ServerConfig) -> Void
    
    var body: some View {
        VStack(spacing: 20) {
            Text("Connection Settings")
                .font(.headline)
            
            Form {
                TextField("SSH Alias:", text: $config.sshAlias)
                TextField("Remote Path:", text: $config.remotePath)
                TextField("Mount Path:", text: $config.mountPath)
            }
            .formStyle(.grouped)
            
            HStack {
                Button("Cancel") {
                    dismiss()
                }
                .keyboardShortcut(.cancelAction)
                
                Spacer()
                
                Button("Save") {
                    onSave(config)
                }
                .keyboardShortcut(.defaultAction)
                .disabled(config.sshAlias.isEmpty)
            }
        }
        .padding()
        .frame(width: 350, height: 250)
    }
}
