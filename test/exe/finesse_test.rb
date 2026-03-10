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

  private

  def run_shim_with_db(db_path, *extra_args)
    Dir.mktmpdir do |dir|
      setup_test_dir(dir, db_path)
      output, status = Open3.capture2(
        { 'FINESSE_SIGNING_KEY' => 'abc123' },
        'ruby', File.join(dir, 'finesse'), *extra_args, chdir: dir
      )

      assert_predicate status, :success?, "shim should exit 0, got: #{status.exitstatus}"
      output
    end
  end

  def setup_test_dir(dir, db_path)
    write_database_yml(dir, db_path)
    fake_bin = write_fake_binary(dir)
    write_shim(dir, fake_bin)
  end

  def write_database_yml(dir, db_path)
    config_dir = File.join(dir, 'config')
    FileUtils.mkdir_p(config_dir)
    File.write(File.join(config_dir, 'database.yml'), <<~YAML)
      development:
        cable:
          database: #{db_path}
    YAML
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
      args = ARGV.dup
      unless args.any? { |a| a.start_with?("--db-path") }
        db_path = Finesse::Platforms.cable_db_path
        args.unshift("--db-path", db_path) if db_path
      end
      exec(Finesse::Platforms.executable, *args)
    RUBY
  end
end
