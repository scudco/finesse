# frozen_string_literal: true

require 'rails/railtie'
require_relative 'streams_helper'

module Finesse
  # Adds `config.finesse.url`, the `finesse_stream_from` view helper, and the
  # `finesse` JavaScript module (served through the asset pipeline, pinned for
  # importmap-rails).
  class Railtie < Rails::Railtie
    JS_DIR = File.expand_path('../../app/assets/javascripts', __dir__)
    IMPORTMAP = File.expand_path('../../config/importmap.rb', __dir__)

    config.finesse = ActiveSupport::OrderedOptions.new
    config.finesse.url = ENV['FINESSE_URL'] || "http://localhost:#{ENV.fetch('FINESSE_PORT', 4000)}"

    initializer 'finesse.helper' do
      ActiveSupport.on_load(:action_view) { include Finesse::StreamsHelper }
    end

    initializer 'finesse.assets' do |app|
      if app.config.respond_to?(:assets)
        app.config.assets.paths << JS_DIR
        app.config.assets.precompile += %w[finesse.js]
      end
    end

    # Must run before importmap-rails reads its paths, as that gem's README
    # documents for engines.
    initializer 'finesse.importmap', before: 'importmap' do |app|
      app.config.importmap.paths << IMPORTMAP if app.config.respond_to?(:importmap)
    end
  end
end
