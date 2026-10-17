# frozen_string_literal: true

require 'puma/plugin'
require_relative '../../finesse/puma_plugin'

Puma::Plugin.create do
  def start(launcher)
    Finesse::PumaPlugin.new(launcher).start
  end
end
