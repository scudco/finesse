# frozen_string_literal: true

require "test_helper"
require "tmpdir"
require "fileutils"

class TestPlatforms < Minitest::Test
  def test_platform_returns_known_platform
    platform = Finesse::Platforms.platform
    assert_includes Finesse::Platforms::PLATFORMS.keys, platform
  end

  def test_executable_raises_when_binary_missing
    with_executable_stub("/nonexistent/path/finesse") do
      assert_raises(Finesse::Platforms::UnsupportedPlatformError) do
        Finesse::Platforms.executable
      end
    end
  end

  def test_executable_returns_path_when_binary_exists
    Dir.mktmpdir do |dir|
      bin_path = File.join(dir, "exe", Finesse::Platforms.platform, "finesse")
      FileUtils.mkdir_p(File.dirname(bin_path))
      File.write(bin_path, "")

      with_executable_stub(bin_path) do
        assert_equal bin_path, Finesse::Platforms.executable
      end
    end
  end

  def test_cable_db_path_from_database_yml
    with_database_yml("development" => { "cable" => { "database" => "storage/development_cable.sqlite3" } }) do
      assert_equal "storage/development_cable.sqlite3", Finesse::Platforms.cable_db_path
    end
  end

  def test_cable_db_path_respects_rails_env
    config = {
      "development" => { "cable" => { "database" => "storage/dev.sqlite3" } },
      "production"  => { "cable" => { "database" => "storage/prod.sqlite3" } },
    }
    with_database_yml(config) do
      with_env("RAILS_ENV" => "production") do
        assert_equal "storage/prod.sqlite3", Finesse::Platforms.cable_db_path
      end
    end
  end

  def test_cable_db_path_with_yaml_aliases
    yaml = <<~YAML
      default: &default
        adapter: sqlite3
        timeout: 5000
      development:
        cable:
          <<: *default
          database: storage/development_cable.sqlite3
    YAML
    with_raw_database_yml(yaml) do
      assert_equal "storage/development_cable.sqlite3", Finesse::Platforms.cable_db_path
    end
  end

  def test_cable_db_path_returns_nil_when_no_config
    Dir.mktmpdir do |dir|
      Dir.chdir(dir) do
        assert_nil Finesse::Platforms.cable_db_path
      end
    end
  end

  def test_cable_db_path_returns_nil_when_no_cable_section
    with_database_yml("development" => { "primary" => { "database" => "storage/dev.sqlite3" } }) do
      assert_nil Finesse::Platforms.cable_db_path
    end
  end

  private

  def with_executable_stub(path)
    original = Finesse::Platforms.method(:executable)
    quietly do
      Finesse::Platforms.define_singleton_method(:executable) do
        raise Finesse::Platforms::UnsupportedPlatformError, "not found at #{path}" unless File.exist?(path)
        path
      end
    end
    yield
  ensure
    quietly { Finesse::Platforms.define_singleton_method(:executable, original) }
  end

  def quietly
    old = $VERBOSE
    $VERBOSE = nil
    yield
  ensure
    $VERBOSE = old
  end

  def with_database_yml(config)
    require "yaml"
    with_raw_database_yml(YAML.dump(config)) { yield }
  end

  def with_raw_database_yml(yaml_content)
    Dir.mktmpdir do |dir|
      config_dir = File.join(dir, "config")
      FileUtils.mkdir_p(config_dir)
      File.write(File.join(config_dir, "database.yml"), yaml_content)
      Dir.chdir(dir) { yield }
    end
  end

  def with_env(vars)
    originals = vars.map { |k, _| [k, ENV[k]] }.to_h
    vars.each { |k, v| ENV[k] = v }
    yield
  ensure
    originals.each { |k, v| v ? ENV[k] = v : ENV.delete(k) }
  end
end
