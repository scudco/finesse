module Finesse
  module Platforms
    # Maps Ruby platform identifiers to Go binary subdirectories.
    PLATFORMS = {
      "arm64-darwin"   => "arm64-darwin",
      "x86_64-darwin"  => "x86_64-darwin",
      "aarch64-linux"  => "aarch64-linux",
      "x86_64-linux"   => "x86_64-linux",
    }.freeze

    class UnsupportedPlatformError < StandardError; end

    class << self
      def platform
        cpu = Gem::Platform.local.cpu
        os = Gem::Platform.local.os

        case [cpu, os]
        when ["arm64", "darwin"]
          "arm64-darwin"
        when ["x86_64", "darwin"]
          "x86_64-darwin"
        when ["aarch64", "linux"]
          "aarch64-linux"
        when ["x86_64", "linux"]
          "x86_64-linux"
        else
          raise UnsupportedPlatformError,
            "Finesse does not support #{cpu}-#{os}. " \
            "Supported platforms: #{PLATFORMS.keys.join(", ")}"
        end
      end

      def executable
        exe = File.expand_path(
          File.join("..", "..", "exe", platform, "finesse"),
          __dir__
        )

        unless File.exist?(exe)
          raise UnsupportedPlatformError,
            "Finesse binary not found at #{exe}. " \
            "Run `rake build:go` to compile from source."
        end

        exe
      end

      # Resolve the cable database path from config/database.yml.
      # Returns nil if the file doesn't exist or no cable DB is configured.
      def cable_db_path
        config_path = File.join(Dir.pwd, "config", "database.yml")
        return nil unless File.exist?(config_path)

        require "yaml"
        require "erb"

        yaml = ERB.new(File.read(config_path)).result
        config = YAML.safe_load(yaml, permitted_classes: [Symbol], aliases: true)
        env = ENV.fetch("RAILS_ENV", "development")

        # Multi-DB layout: { "development" => { "cable" => { "database" => "..." } } }
        env_config = config[env]
        return nil unless env_config.is_a?(Hash)

        cable_config = env_config["cable"]
        return nil unless cable_config.is_a?(Hash)

        cable_config["database"]
      end
    end
  end
end
