require_relative "lib/finesse/version"

Gem::Specification.new do |spec|
  spec.name        = "finesse"
  spec.version     = Finesse::VERSION
  spec.authors     = ["scudco"]
  spec.summary     = "SSE server for ActionCable in Rails"
  spec.description = "Finesse replaces ActionCable's default WebSocket transport with a lightweight Go binary"
  spec.homepage    = "https://github.com/scudco/finesse"
  spec.license     = "MIT"

  spec.required_ruby_version = ">= 4.0"

  spec.files = Dir["lib/**/*", "LICENSE", "README.md"]
  spec.require_paths = ["lib"]
end
