# frozen_string_literal: true

require 'test_helper'
require 'rails/generators/test_case'
require 'generators/finesse/systemd/systemd_generator'

class TestSystemdGenerator < Rails::Generators::TestCase
  tests Finesse::Generators::SystemdGenerator
  destination File.expand_path('../../../../tmp/systemd_generator', __dir__)
  setup :prepare_destination

  def test_writes_unit_that_runs_finesse_from_the_app_root
    run_generator %w[--origin https://app.test --user rails]

    assert_file 'config/finesse.service' do |unit|
      assert_includes unit, "WorkingDirectory=#{destination_root}\n"
      assert_includes unit, "User=rails\n"
      assert_includes unit, 'ExecStart=/usr/bin/env bundle exec finesse --allow-origin https://app.test'
      assert_includes unit, "Restart=always\n"
      assert_includes unit, "Environment=RAILS_ENV=production\n"
      assert_includes unit, 'FINESSE_SIGNING_KEY'
    end
  end

  def test_defaults_to_placeholder_origin_and_deploy_user
    run_generator

    assert_file 'config/finesse.service', %r{User=deploy\n.*--allow-origin https://example\.com}m
  end
end
