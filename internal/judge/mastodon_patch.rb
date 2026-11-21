# Lets Mastodon fetch actors whose id is a bare origin, as microblog.pub's id
# is (https://a.test).
#
# Since v4.4.27, Mastodon requires that the URL of a fetched document equal the
# URL that it requested (app/helpers/json_ld_helper.rb,
# fetch_resource_without_id_validation). But Mastodon changes the URL to
# https://a.test/ before the request. So a server that didn't meet the
# account before can't resolve the account or fetch its key.
#
# This patch changes what Mastodon accepts, never what it sends. mastodon.go
# mounts it into config/initializers.
module JudgeFetchNormalizedURI
  def fetch_resource_without_id_validation(uri, **options)
    super(Addressable::URI.parse(uri).normalize.to_s, **options)
  end
end

Rails.application.config.to_prepare do
  JsonLdHelper.prepend(JudgeFetchNormalizedURI)
end
