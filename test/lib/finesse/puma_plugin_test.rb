# frozen_string_literal: true

require 'test_helper'
require 'finesse/puma_plugin'
require 'tmpdir'
require 'fileutils'
require 'yaml'

class TestPumaPlugin < Minitest::Test
  def test_build_config_assembles_args_for_sqlite_config
    with_database_yml('development' => { 'cable' => { 'database' => 'storage/dev_cable.sqlite3' } }) do
      config = build_config('FINESSE_SIGNING_KEY' => 'deadbeef')

      refute_nil config
      assert_equal 'deadbeef', config[:env]['FINESSE_SIGNING_KEY']
      assert_equal 'storage/dev_cable.sqlite3', flag_value(config[:args], '--db-path')
      assert_equal 'http://localhost:3000', flag_value(config[:args], '--allow-origin')
    end
  end

  def test_build_config_passes_port_from_env
    config = build_config('FINESSE_SIGNING_KEY' => 'deadbeef', 'FINESSE_PORT' => '4477')

    assert_equal '4477', flag_value(config[:args], '--port')
  end

  def test_build_config_omits_port_when_unset
    config = build_config('FINESSE_SIGNING_KEY' => 'deadbeef')

    refute_includes config[:args], '--port'
  end

  def test_origin_parsing_multiple_comma_separated
    config = build_config(
      'FINESSE_SIGNING_KEY' => 'deadbeef',
      'FINESSE_ALLOW_ORIGIN' => 'http://a.test, http://b.test ,http://c.test'
    )

    assert_equal %w[http://a.test http://b.test http://c.test], flag_values(config[:args], '--allow-origin')
  end

  def test_default_origin_in_development
    config = build_config('FINESSE_SIGNING_KEY' => 'deadbeef', 'RAILS_ENV' => 'development')

    assert_equal ['http://localhost:3000'], flag_values(config[:args], '--allow-origin')
  end

  def test_default_origin_via_rack_env
    config = build_config('FINESSE_SIGNING_KEY' => 'deadbeef', 'RACK_ENV' => 'development')

    assert_equal ['http://localhost:3000'], flag_values(config[:args], '--allow-origin')
  end

  def test_missing_origin_outside_development_skips_start
    logs = []
    config = build_config({ 'FINESSE_SIGNING_KEY' => 'deadbeef', 'RAILS_ENV' => 'production' }, logs)

    assert_nil config
    assert_match(/FINESSE_ALLOW_ORIGIN/, logs.join)
  end

  def test_missing_signing_key_skips_start_without_raising
    stub_key_error = ->(*) { raise LoadError, 'cannot load such file -- turbo-rails' }
    Finesse.stub(:signing_key, stub_key_error) do
      logs = []
      config = build_config({}, logs)

      assert_nil config
      assert_match(/signing key/, logs.join)
    end
  end

  def test_env_key_takes_precedence_over_derivation
    stub_key_error = ->(*) { raise LoadError, 'should not be called' }
    Finesse.stub(:signing_key, stub_key_error) do
      config = build_config('FINESSE_SIGNING_KEY' => 'deadbeef')

      assert_equal 'deadbeef', config[:env]['FINESSE_SIGNING_KEY']
    end
  end

  def test_derives_signing_key_when_env_unset
    Finesse.stub(:signing_key, ->(*) { 'derived-key' }) do
      config = build_config({})

      assert_equal 'derived-key', config[:env]['FINESSE_SIGNING_KEY']
    end
  end

  private

  def build_config(env, logs = [])
    log = ->(msg) { logs << msg }
    # Platforms.cable_db_args reads RAILS_ENV from the real ENV; pin it for isolation.
    with_env('RAILS_ENV' => nil) { Finesse::PumaPlugin.build_config(env, log: log) }
  end

  def flag_value(args, flag)
    flag_values(args, flag).first
  end

  def flag_values(args, flag)
    values = []
    args.each_with_index { |arg, i| values << args[i + 1] if arg == flag }
    values
  end

  def with_database_yml(config, &)
    Dir.mktmpdir do |dir|
      FileUtils.mkdir_p(File.join(dir, 'config'))
      File.write(File.join(dir, 'config', 'database.yml'), YAML.dump(config))
      Dir.chdir(dir, &)
    end
  end

  def with_env(vars)
    original = vars.keys.to_h { |key| [key, ENV.fetch(key, nil)] }
    vars.each { |key, value| value.nil? ? ENV.delete(key) : ENV[key] = value }
    yield
  ensure
    original.each { |key, value| value.nil? ? ENV.delete(key) : ENV[key] = value }
  end
end
