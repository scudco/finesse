# frozen_string_literal: true

require 'test_helper'
require 'rails/generators/test_case'
require 'generators/finesse/install/install_generator'

class TestInstallGenerator < Rails::Generators::TestCase
  tests Finesse::Generators::InstallGenerator
  destination File.expand_path('../../../../tmp/install_generator', __dir__)
  setup :prepare_destination

  def test_appends_plugin_to_puma_config
    write_puma "port 3000\n"
    run_generator

    assert_file 'config/puma.rb', /\Aport 3000\n\nplugin :finesse\n\z/
  end

  def test_running_twice_adds_the_plugin_once
    write_puma "port 3000\n"
    2.times { run_generator }

    assert_equal 1, File.read(File.join(destination_root, 'config/puma.rb')).scan('plugin :finesse').size
  end

  def test_skips_when_plugin_is_already_present_with_a_condition
    write_puma "plugin :finesse if ENV['FINESSE_IN_PUMA']\n"
    run_generator

    assert_equal 1, File.read(File.join(destination_root, 'config/puma.rb')).scan('plugin :finesse').size
  end

  def test_appends_when_plugin_is_only_commented_out
    write_puma "# plugin :finesse\n"
    run_generator

    assert_file 'config/puma.rb', /^plugin :finesse$/
  end

  def test_warns_and_continues_without_puma_config
    output = run_generator

    assert_match(%r{config/puma.rb not found}, output)
    assert_no_file 'config/puma.rb'
    assert_file 'config/initializers/finesse.rb', /config\.finesse\.url/
  end

  def test_initializer_is_not_overwritten
    write_puma ''
    FileUtils.mkdir_p(File.join(destination_root, 'config/initializers'))
    File.write(File.join(destination_root, 'config/initializers/finesse.rb'), "# mine\n")
    run_generator

    assert_file 'config/initializers/finesse.rb', "# mine\n"
  end

  def test_initializer_documents_allow_origin
    run_generator

    assert_file 'config/initializers/finesse.rb', /FINESSE_ALLOW_ORIGIN/
  end

  private

  def write_puma(content)
    FileUtils.mkdir_p(File.join(destination_root, 'config'))
    File.write(File.join(destination_root, 'config/puma.rb'), content)
  end
end
