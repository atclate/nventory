class UtilizationMetricName < ActiveRecord::Base
  acts_as_authorizable
  acts_as_audited
  
  named_scope :def_scope
  
  acts_as_reportable
  acts_as_commentable
  
  has_many :nodes
  
  validates_presence_of :name
  validates_uniqueness_of :name
  
  def self.default_search_attribute
    'name'
  end
 
end
