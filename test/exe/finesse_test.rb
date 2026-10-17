# frozen_string_literal: true

require 'test_helper'
require 'open3'
require 'tmpdir'
require 'fileutils'

class TestExe < Minitest::Test
  EXE_PATH = File.expand_path('../../exe/finesse', __dir__)

  def test_exe_exists_and_is_executable
    assert_path_exists EXE_PATH, 'exe/finesse should exist'
    assert File.executable?(EXE_PATH), 'exe/finesse should be executable'
  end

  def test_exe_injects_db_path_from_database_yml
    output = run_shim_with_db('storage/dev_cable.sqlite3')

    assert_includes output, '--db-path storage/dev_cable.sqlite3'
  end

  def test_exe_does_not_override_explicit_db_path
    output = run_shim_with_db('storage/should_not_use.sqlite3', '--db-path', 'my/custom.sqlite3')

    assert_includes output, '--db-path my/custom.sqlite3'
    refute_includes output, 'should_not_use'
  end

  def test_exe_injects_db_url_for_postgres
    output = run_shim_with_yaml(<<~YAML)
      development:
        cable:
          adapter: postgresql
          database: app_cable
          host: localhost
          username: app
    YAML

    assert_includes output, '--db-url dbname=app_cable host=localhost user=app'
    refute_includes output, '--db-path'
  end

  def test_exe_does_not_override_explicit_db_url
    output = run_shim_with_yaml(<<~YAML, '--db-url', 'dbname=custom')
      development:
        cable:
          adapter: postgresql
          database: should_not_use
    YAML

    assert_includes output, '--db-url dbname=custom'
    refute_includes output, 'should_not_use'
  end

  private

  def run_shim_with_db(db_path, *extra_args)
    run_shim_with_yaml(<<~YAML, *extra_args)
      development:
        cable:
          database: #{db_path}
    YAML
  end

  def run_shim_with_yaml(yaml, *extra_args)
    Dir.mktmpdir do |dir|
      setup_test_dir(dir, yaml)
      output, status = Open3.capture2(
        { 'FINESSE_SIGNING_KEY' => 'abc123' },
        'ruby', File.join(dir, 'finesse'), *extra_args, chdir: dir
      )

      assert_predicate status, :success?, "shim should exit 0, got: #{status.exitstatus}"
      output
    end
  end

  def setup_test_dir(dir, yaml)
    config_dir = File.join(dir, 'config')
    FileUtils.mkdir_p(config_dir)
    File.write(File.join(config_dir, 'database.yml'), yaml)
    write_shim(dir, write_fake_binary(dir))
  end

  def write_fake_binary(dir)
    bin_dir = File.join(dir, 'exe', Finesse::Platforms.platform)
    FileUtils.mkdir_p(bin_dir)
    fake_bin = File.join(bin_dir, 'finesse')
    File.write(fake_bin, "#!/bin/sh\necho \"$@\"\n")
    FileUtils.chmod(0o755, fake_bin)
    fake_bin
  end

  def write_shim(dir, fake_bin)
    lib_path = File.expand_path('lib', __dir__)
    File.write(File.join(dir, 'finesse'), shim_code(lib_path, fake_bin))
  end

  def shim_code(lib_path, fake_bin)
    <<~RUBY
      $LOAD_PATH.unshift("#{lib_path}")
      require "finesse/platforms"
      Finesse::Platforms.define_singleton_method(:executable) { "#{fake_bin}" }
      load "#{EXE_PATH}"
    RUBY
  end
end
