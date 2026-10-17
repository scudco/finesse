# frozen_string_literal: true

require 'rails/railtie'
require_relative 'streams_helper'

module Finesse
  # Adds `config.finesse.url`, the `finesse_stream_from` view helper, and
  # the `finesse:signing_key` rake task.
  class Railtie < Rails::Railtie
    config.finesse = ActiveSupport::OrderedOptions.new
    config.finesse.url = ENV['FINESSE_URL'] || "http://localhost:#{ENV.fetch('FINESSE_PORT', 4000)}"

    initializer 'finesse.helper' do
      ActiveSupport.on_load(:action_view) { include Finesse::StreamsHelper }
    end

    rake_tasks do
      namespace :finesse do
        desc 'Print the hex signing key for FINESSE_SIGNING_KEY (for a Finesse process that runs without Rails)'
        task signing_key: :environment do
          puts Finesse.signing_key
        end
      end
    end
  end
end
