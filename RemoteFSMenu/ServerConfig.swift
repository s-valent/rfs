import Foundation

struct ServerConfig: Codable, Identifiable, Equatable {
    var id: UUID = UUID()
    var sshAlias: String
    var remotePath: String
    var mountPath: String
    
    init(sshAlias: String = "", remotePath: String = "~/", mountPath: String = "") {
        self.sshAlias = sshAlias
        self.remotePath = remotePath
        self.mountPath = mountPath
    }
}

class ServerConfigStore: ObservableObject {
    @Published var configs: [ServerConfig] = []
    
    private let userDefaultsKey = "serverConfigs"
    
    init() {
        load()
    }
    
    func load() {
        if let data = UserDefaults.standard.data(forKey: userDefaultsKey),
           let decoded = try? JSONDecoder().decode([ServerConfig].self, from: data) {
            configs = decoded
        }
    }
    
    func save() {
        if let encoded = try? JSONEncoder().encode(configs) {
            UserDefaults.standard.set(encoded, forKey: userDefaultsKey)
        }
    }
    
    func add(_ config: ServerConfig) {
        configs.append(config)
        save()
    }
    
    func remove(at index: Int) {
        guard index < configs.count else { return }
        configs.remove(at: index)
        save()
    }
    
    func update(_ config: ServerConfig) {
        if let i = configs.firstIndex(where: { $0.id == config.id }) {
            configs[i] = config
            save()
        }
    }
}
