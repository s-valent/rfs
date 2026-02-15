import SwiftUI

enum HealthStatus {
    case stopped
    case starting
    case running
    case error(String)
}

protocol MenuViewDelegate: AnyObject {
    func startServer(config: ServerConfig)
    func stopServer(config: ServerConfig)
    func checkHealth()
    func getStatus(for configId: UUID) -> HealthStatus
}

struct MenuView: View {
    weak var delegate: MenuViewDelegate?
    @ObservedObject var serverStore: ServerStore
    @StateObject private var store = ServerConfigStore()
    @State private var isAdding = false
    @State private var editingConfig: ServerConfig?
    @State private var refreshTimer: Timer?
    @State private var showingError = false
    @State private var errorMessage = ""
    
    var body: some View {
        VStack(spacing: 0) {
            if store.configs.isEmpty {
                emptyStateView
            } else {
                configsList
            }
            
            Divider()
            
            footerView
        }
        .frame(width: 320, height: 400)
        .background(Color(NSColor.windowBackgroundColor))
        .alert("Error", isPresented: $showingError) {
            Button("OK", role: .cancel) { }
        } message: {
            Text(errorMessage)
        }
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
        .onAppear {
            refreshTimer = Timer.scheduledTimer(withTimeInterval: 0.5, repeats: true) { _ in
                self.serverStore.objectWillChange.send()
                delegate?.checkHealth()
            }
        }
        .onDisappear {
            refreshTimer?.invalidate()
        }
    }
    
    private var emptyStateView: some View {
        VStack(spacing: 12) {
            Spacer()
            Image(systemName: "externaldrive.badge.questionmark")
                .font(.system(size: 40))
                .foregroundColor(.secondary)
            Text("No connections")
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
                    healthStatus: serverStore.servers[config.id]?.status ?? .stopped,
                    onStart: {
                        delegate?.startServer(config: config)
                    },
                    onStop: {
                        delegate?.stopServer(config: config)
                    },
                    onEdit: {
                        editingConfig = config
                    },
                    onDelete: {
                        delegate?.stopServer(config: config)
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
    let healthStatus: HealthStatus
    let onStart: () -> Void
    let onStop: () -> Void
    let onEdit: () -> Void
    let onDelete: () -> Void
    
    private var isActive: Bool {
        if case .running = healthStatus { return true }
        return false
    }
    
    private var statusText: String {
        switch healthStatus {
        case .stopped: return "Stopped"
        case .starting: return "Starting..."
        case .running: return "Running"
        case .error(let msg): return msg
        }
    }
    
    var body: some View {
        VStack(alignment: .leading, spacing: 8) {
            HStack {
                Text(config.sshAlias)
                    .font(.headline)
                Spacer()
                Text(statusText)
                    .font(.caption)
                    .foregroundColor(statusColor)
            }
            
            HStack {
                Text(config.remotePath)
                    .font(.caption)
                    .foregroundColor(.secondary)
                Spacer()
                if isActive {
                    Button("Stop") {
                        onStop()
                    }
                    .buttonStyle(.bordered)
                } else {
                    Button("Start") {
                        onStart()
                    }
                    .buttonStyle(.bordered)
                }
            }
            
            HStack {
                Button("Edit") {
                    onEdit()
                }
                .font(.caption)
                .buttonStyle(.borderless)
                Button("Delete") {
                    onDelete()
                }
                .font(.caption)
                .buttonStyle(.borderless)
            }
        }
        .padding(.vertical, 8)
    }
    
    private var statusColor: Color {
        switch healthStatus {
        case .stopped: return .secondary
        case .starting: return .orange
        case .running: return .green
        case .error: return .red
        }
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
                TextField("Mount Path (optional):", text: $config.mountPath)
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
