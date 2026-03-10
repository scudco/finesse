# frozen_string_literal: true

require "test_helper"
require "open3"
require "tmpdir"
require "fileutils"

class TestExe < Minitest::Test
  EXE_PATH = File.expand_path("../../exe/finesse", __dir__)

  def test_exe_exists_and_is_executable
    assert File.exist?(EXE_PATH), "exe/finesse should exist"
    assert File.executable?(EXE_PATH), "exe/finesse should be executable"
  end

  def test_exe_injects_db_path_from_database_yml
    Dir.mktmpdir do |dir|
      config_dir = File.join(dir, "config")
      FileUtils.mkdir_p(config_dir)
      File.write(File.join(config_dir, "database.yml"), <<~YAML)
        development:
          cable:
            database: storage/dev_cable.sqlite3
      YAML

      # Use a fake binary that prints its args instead of running the real server.
      platform = Finesse::Platforms.platform
      bin_dir = File.join(dir, "exe", platform)
      FileUtils.mkdir_p(bin_dir)
      fake_bin = File.join(bin_dir, "finesse")
      File.write(fake_bin, <<~SH)
        #!/bin/sh
        echo "$@"
      SH
      FileUtils.chmod(0o755, fake_bin)

      # Write a shim that uses our fake binary directory.
      shim = File.join(dir, "finesse")
      File.write(shim, <<~RUBY)
        #!/usr/bin/env ruby
        $LOAD_PATH.unshift("#{File.expand_path("lib", __dir__)}")
        require "finesse/platforms"

        # Override executable to point at fake binary.
        Finesse::Platforms.define_singleton_method(:executable) { "#{fake_bin}" }

        args = ARGV.dup
        unless args.any? { |a| a.start_with?("--db-path") }
          db_path = Finesse::Platforms.cable_db_path
          args.unshift("--db-path", db_path) if db_path
        end

        exec(Finesse::Platforms.executable, *args)
      RUBY

      # Run the shim from the tmpdir (so it finds config/database.yml).
      output, status = Open3.capture2(
        { "FINESSE_SIGNING_KEY" => "abc123" },
        "ruby", shim,
        chdir: dir
      )

      assert status.success?, "shim should exit 0"
      assert_includes output, "--db-path storage/dev_cable.sqlite3"
    end
  end

  def test_exe_does_not_override_explicit_db_path
    Dir.mktmpdir do |dir|
      config_dir = File.join(dir, "config")
      FileUtils.mkdir_p(config_dir)
      File.write(File.join(config_dir, "database.yml"), <<~YAML)
        development:
          cable:
            database: storage/should_not_use.sqlite3
      YAML

      platform = Finesse::Platforms.platform
      bin_dir = File.join(dir, "exe", platform)
      FileUtils.mkdir_p(bin_dir)
      fake_bin = File.join(bin_dir, "finesse")
      File.write(fake_bin, <<~SH)
        #!/bin/sh
        echo "$@"
      SH
      FileUtils.chmod(0o755, fake_bin)

      shim = File.join(dir, "finesse")
      File.write(shim, <<~RUBY)
        #!/usr/bin/env ruby
        $LOAD_PATH.unshift("#{File.expand_path("lib", __dir__)}")
        require "finesse/platforms"

        Finesse::Platforms.define_singleton_method(:executable) { "#{fake_bin}" }

        args = ARGV.dup
        unless args.any? { |a| a.start_with?("--db-path") }
          db_path = Finesse::Platforms.cable_db_path
          args.unshift("--db-path", db_path) if db_path
        end

        exec(Finesse::Platforms.executable, *args)
      RUBY

      output, status = Open3.capture2(
        { "FINESSE_SIGNING_KEY" => "abc123" },
        "ruby", shim, "--db-path", "my/custom.sqlite3",
        chdir: dir
      )

      assert status.success?
      assert_includes output, "--db-path my/custom.sqlite3"
      refute_includes output, "should_not_use"
    end
  end
end
