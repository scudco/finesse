# frozen_string_literal: true

require 'test_helper'
require 'rails'
require 'rake'
require 'stringio'
require 'finesse/railtie'

# Stand-in for turbo-rails so the test needn't boot its engine.
module Turbo; end
$LOADED_FEATURES << 'turbo-rails.rb'

class TestRailtieTasks < Minitest::Test
  def setup
    @rake = Rake::Application.new
    Rake.application = @rake
    Rake::Task.define_task(:environment)
    Finesse::Railtie.instance.send(:run_tasks_blocks, nil)
  end

  def teardown
    Rake.application = nil
  end

  def test_signing_key_task_prints_the_hex_key
    Turbo.define_singleton_method(:signed_stream_verifier_key) { 'abc' }
    out, = capture_io { @rake['finesse:signing_key'].invoke }

    assert_equal "#{'abc'.unpack1('H*')}\n", out
  end
end
