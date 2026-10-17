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

      # CLI args locating the cable database: --db-url when a URL is
      # configured, else --db-path from config/database.yml.
      # Returns an empty array when neither resolves.
      def cable_db_args
        url = cable_db_url
        return ['--db-url', url] unless url.nil? || url.empty?

        path = cable_db_path
        path ? ['--db-path', path] : []
      end

      # Resolve the cable database section from config/database.yml.
      # Returns nil if the file doesn't exist or no cable DB is configured.
      def cable_db_config
        config = load_database_yml
        return nil unless config

        env_config = config.dig(ENV.fetch('RAILS_ENV', 'development'), 'cable')
        env_config.is_a?(Hash) ? env_config : nil
      end

      # SQLite database path, or nil when the cable DB is PostgreSQL.
      def cable_db_path
        config = cable_db_config
        return nil if config.nil? || postgres?(config)

        config['database']
      end

      # libpq connection string for the cable DB, or nil unless PostgreSQL.
      def cable_db_url
        config = cable_db_config
        return nil if config.nil? || !postgres?(config)
        return config['url'] if config['url']

        dsn = dsn_pairs(config).compact.map { |k, v| "#{k}=#{dsn_quote(v)}" }.join(' ')
        dsn.empty? ? nil : dsn
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

      def postgres?(config)
        config['adapter'].to_s.start_with?('postgres')
      end

      def dsn_pairs(config)
        { 'dbname' => config['database'], 'host' => config['host'], 'port' => config['port'],
          'user' => config['username'], 'password' => config['password'] }
      end

      # Quote a libpq DSN value when it contains spaces, quotes, or is empty.
      def dsn_quote(value)
        s = value.to_s
        return s unless s.empty? || s.match?(/[\s'\\]/)

        "'#{s.gsub(/[\\']/) { |c| "\\#{c}" }}'"
      end
    end
  end
end
