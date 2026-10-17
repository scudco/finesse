# frozen_string_literal: true

require 'test_helper'
require 'rails'
require 'finesse/railtie'

class TestRailtie < Minitest::Test
  Paths = Struct.new(:paths, :precompile)
  Config = Struct.new(:importmap, :assets)

  def test_pins_finesse_for_importmap_and_serves_the_module
    config = Config.new(Paths.new([]), Paths.new([], []))
    run_initializers(config)

    assert_equal [Finesse::Railtie::IMPORTMAP], config.importmap.paths
    assert_equal [Finesse::Railtie::JS_DIR], config.assets.paths
    assert_equal %w[finesse.js], config.assets.precompile
  end

  def test_importmap_and_module_exist
    assert_match(/^pin 'finesse'$/, File.read(Finesse::Railtie::IMPORTMAP))
    assert_path_exists File.join(Finesse::Railtie::JS_DIR, 'finesse.js')
  end

  def test_skips_importmap_and_assets_when_the_app_has_neither
    run_initializers(Struct.new(:other).new)
  end

  private

  def run_initializers(config)
    app = Struct.new(:config).new(config)
    Finesse::Railtie.instance.initializers.grep(->(i) { i.name.start_with?('finesse.') && i.name != 'finesse.helper' })
                    .each { |i| i.run(app) }
  end
end
