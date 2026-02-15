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
    @State private var editingConfig: ServerConfig?
    @State private var isEditingNew = false
    @State private var refreshTimer: Timer?
    
    var body: some View {
        VStack(spacing: 0) {
            if store.configs.isEmpty && editingConfig == nil {
                emptyStateView
            } else {
                configsList
            }
            
            Divider()
            
            footerView
        }
        .frame(width: 320, height: 280)
        .background(Color.clear)
        .onAppear {
            refreshTimer = Timer.scheduledTimer(withTimeInterval: 0.5, repeats: true) { _ in
                self.serverStore.objectWillChange.send()
                delegate?.checkHealth()
            }
            DispatchQueue.main.async {
                NSApp.keyWindow?.makeFirstResponder(nil)
            }
        }
        .onDisappear {
            refreshTimer?.invalidate()
        }
    }
    
    private var emptyStateView: some View {
        VStack(spacing: 4) {
            Spacer()
            Image(systemName: "externaldrive.badge.questionmark")
                .font(.system(size: 20))
                .foregroundColor(.secondary)
            Text("No connections")
                .font(.caption)
                .foregroundColor(.secondary)
            Button(action: { 
                editingConfig = ServerConfig()
                isEditingNew = true
            }) {
                Image(systemName: "plus")
            }
            .buttonStyle(.borderless)
            Spacer()
        }
    }
    
    private var configsList: some View {
        ScrollView {
            LazyVStack(spacing: 0) {
                ForEach(store.configs) { config in
                    rowView(for: config)
                        .id(config.id)
                }
                
                if isEditingNew, let config = editingConfig {
                    InlineEditRow(
                        config: config,
                        isNew: true,
                        onSave: { saved in
                            store.add(saved)
                            editingConfig = nil
                            isEditingNew = false
                        },
                        onCancel: {
                            editingConfig = nil
                            isEditingNew = false
                        }
                    )
                }
            }
            .padding(.vertical, 4)
        }
    }
    
    @ViewBuilder
    private func rowView(for config: ServerConfig) -> some View {
        if editingConfig?.id == config.id && !isEditingNew {
            InlineEditRow(
                config: config,
                isNew: false,
                onSave: { saved in
                    store.update(saved)
                    editingConfig = nil
                    isEditingNew = false
                },
                onCancel: {
                    editingConfig = nil
                    isEditingNew = false
                }
            )
        } else {
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
                    isEditingNew = false
                },
                onDelete: {
                    if let idx = store.configs.firstIndex(where: { $0.id == config.id }) {
                        store.remove(at: idx)
                    }
                }
            )
        }
    }
    
    private var footerView: some View {
        HStack {
            Button(action: { 
                editingConfig = ServerConfig()
                isEditingNew = true
            }) {
                Image(systemName: "plus")
            }
            .buttonStyle(.borderless)
            
            Spacer()
            
            Button(action: { NSApplication.shared.terminate(nil) }) {
                Image(systemName: "power")
            }
            .buttonStyle(.borderless)
            .keyboardShortcut("q", modifiers: .command)
        }
        .padding(8)
        .onAppear {
            NSApp.windows.first?.makeFirstResponder(nil)
        }
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
        case .starting: return "..."
        case .running: return "Running"
        case .error(let msg): return msg
        }
    }
    
    var body: some View {
        HStack(spacing: 8) {
            VStack(alignment: .leading, spacing: 2) {
                Text(config.sshAlias)
                    .font(.subheadline)
                    .fontWeight(.medium)
                Text(config.remotePath)
                    .font(.caption)
                    .foregroundColor(.secondary)
            }
            
            Spacer()
            
            Text(statusText)
                .font(.caption)
                .foregroundColor(statusColor)
            
            if isActive {
                Button(action: onStop) {
                    Image(systemName: "stop.fill")
                }
                .buttonStyle(.borderless)
            } else {
                Button(action: onStart) {
                    Image(systemName: "play.fill")
                }
                .buttonStyle(.borderless)
            }
            
            Button(action: onEdit) {
                Image(systemName: "pencil")
            }
            .buttonStyle(.borderless)
            .disabled(isActive)
            
            Button(action: onDelete) {
                Image(systemName: "trash")
            }
            .buttonStyle(.borderless)
            .disabled(isActive)
        }
        .padding(.horizontal, 8)
        .padding(.vertical, 8)
        .contentShape(Rectangle())
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

struct InlineEditRow: View {
    let config: ServerConfig
    var isNew: Bool = false
    let onSave: (ServerConfig) -> Void
    let onCancel: () -> Void
    
    @State private var sshAlias: String
    @State private var remotePath: String
    @State private var mountPath: String
    
    init(config: ServerConfig, isNew: Bool, onSave: @escaping (ServerConfig) -> Void, onCancel: @escaping () -> Void) {
        self.config = config
        self.isNew = isNew
        self.onSave = onSave
        self.onCancel = onCancel
        _sshAlias = State(initialValue: config.sshAlias)
        _remotePath = State(initialValue: config.remotePath)
        _mountPath = State(initialValue: config.mountPath)
    }
    
    var body: some View {
        HStack(spacing: 4) {
            VStack(alignment: .leading, spacing: 2) {
                TextField("alias", text: $sshAlias)
                    .textFieldStyle(.plain)
                    .font(.subheadline)
                TextField("path", text: $remotePath)
                    .textFieldStyle(.plain)
                    .font(.caption)
            }
            .frame(width: 150)
            
            TextField("mount", text: $mountPath)
                .textFieldStyle(.plain)
                .font(.caption)
                .frame(width: 70)
            
            Spacer()
            
            Button(action: onCancel) {
                Image(systemName: "xmark")
            }
            .buttonStyle(.borderless)
            
            Button(action: {
                var updated = config
                updated.sshAlias = sshAlias
                updated.remotePath = remotePath
                updated.mountPath = mountPath
                onSave(updated)
            }) {
                Image(systemName: "checkmark")
            }
            .buttonStyle(.borderless)
            .disabled(sshAlias.isEmpty)
        }
        .padding(.horizontal, 8)
        .padding(.vertical, 8)
        .background(Color(NSColor.controlBackgroundColor).opacity(0.5))
    }
}
