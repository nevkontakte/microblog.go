# Lets Mastodon fetch actors whose id is a bare origin, as microblog.pub's is
# (https://a.test). Since v4.4.27, Mastodon requires a fetched document's URL
# to equal the one it asked for (app/helpers/json_ld_helper.rb,
# fetch_resource_without_id_validation), but it normalizes the URL it requests
# to https://a.test/, so a server that hasn't met the account yet can't
# resolve it or fetch its key. This changes what Mastodon accepts, never what
# it sends. Mounted into config/initializers by mastodon.go.
module JudgeFetchNormalizedURI
  def fetch_resource_without_id_validation(uri, **options)
    super(Addressable::URI.parse(uri).normalize.to_s, **options)
  end
end

Rails.application.config.to_prepare do
  JsonLdHelper.prepend(JudgeFetchNormalizedURI)
end
