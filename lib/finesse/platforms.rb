# frozen_string_literal: true

module Finesse
  # Platform detection, binary resolution, and database.yml parsing.
  module Platforms
    SUPPORTED_PLATFORMS = %w[
      arm64-darwin
      x86_64-darwin
      aarch64-linux
      x86_64-linux
    ].freeze

    class UnsupportedPlatformError < StandardError; end

    class << self
      # Root directory of the gem (two levels up from lib/finesse/).
      def root
        File.expand_path('../..', __dir__)
      end

      PLATFORM_MAP = {
        %w[arm64 darwin] => 'arm64-darwin',
        %w[x86_64 darwin] => 'x86_64-darwin',
        %w[aarch64 linux] => 'aarch64-linux',
        %w[x86_64 linux] => 'x86_64-linux'
      }.freeze

      def platform
        cpu = Gem::Platform.local.cpu
        os = Gem::Platform.local.os

        PLATFORM_MAP.fetch([cpu, os]) do
          raise UnsupportedPlatformError,
                "Finesse does not support #{cpu}-#{os}. " \
                "Supported platforms: #{SUPPORTED_PLATFORMS.join(', ')}"
        end
      end

      def executable
        exe = File.join(root, 'exe', platform, 'finesse')

        unless File.exist?(exe)
          raise UnsupportedPlatformError,
                "Finesse binary not found at #{exe}. " \
                'Run `rake build:go` to compile from source.'
        end

        exe
      end

      # Resolve the cable database path from config/database.yml.
      # Returns nil if the file doesn't exist or no cable DB is configured.
      def cable_db_path
        config = load_database_yml
        return nil unless config

        env_config = config.dig(ENV.fetch('RAILS_ENV', 'development'), 'cable')
        env_config['database'] if env_config.is_a?(Hash)
      end

      private

      def load_database_yml
        config_path = File.join(Dir.pwd, 'config', 'database.yml')
        return nil unless File.exist?(config_path)

        require 'yaml'
        require 'erb'

        yaml = ERB.new(File.read(config_path)).result
        YAML.safe_load(yaml, permitted_classes: [Symbol], aliases: true)
      end
    end
  end
end
